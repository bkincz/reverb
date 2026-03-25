package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	dbmodels "github.com/bkincz/reverb/db/models"
	"github.com/bkincz/reverb/internal/roles"
)

// ---------------------------------------------------------------------------
// Key sanitisation
// ---------------------------------------------------------------------------

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._\-/]`)

func sanitiseKey(raw string) string {
	key := strings.ReplaceAll(raw, " ", "_")
	for strings.Contains(key, "../") {
		key = strings.ReplaceAll(key, "../", "")
	}
	key = strings.TrimPrefix(key, "../")
	key = strings.TrimLeft(key, "/")
	key = unsafeChars.ReplaceAllString(key, "_")
	return key
}

// ---------------------------------------------------------------------------
// Response shape
// ---------------------------------------------------------------------------

type VariantResponse struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type mediaResponse struct {
	ID        string                     `json:"id"`
	Filename  string                     `json:"filename"`
	URL       string                     `json:"url"`
	MimeType  string                     `json:"mime_type"`
	Size      int64                      `json:"size"`
	Alt       string                     `json:"alt"`
	Width     int                        `json:"width,omitempty"`
	Height    int                        `json:"height,omitempty"`
	Variants  map[string]VariantResponse `json:"variants,omitempty"`
	CreatedAt time.Time                  `json:"created_at"`
}

func toResponse(m *dbmodels.Media, adapter Adapter) mediaResponse {
	resp := mediaResponse{
		ID:        m.ID,
		Filename:  m.Filename,
		URL:       adapter.URL(m.StorageKey),
		MimeType:  m.MimeType,
		Size:      m.Size,
		Alt:       m.Alt,
		Width:     m.Width,
		Height:    m.Height,
		CreatedAt: m.CreatedAt,
	}

	if len(m.Variants) > 0 {
		var stored map[string]StoredVariant
		if err := json.Unmarshal(m.Variants, &stored); err == nil && len(stored) > 0 {
			resp.Variants = make(map[string]VariantResponse, len(stored))
			for name, sv := range stored {
				resp.Variants[name] = VariantResponse{
					URL:    adapter.URL(sv.Key),
					Width:  sv.Width,
					Height: sv.Height,
				}
			}
		}
	}

	return resp
}

// ---------------------------------------------------------------------------
// HandleUpload — POST /_reverb/storage/upload
// ---------------------------------------------------------------------------

func HandleUpload(db *bun.DB, adapter Adapter, proc ImageProcessor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)

		if err := r.ParseMultipartForm(32 << 20); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "could not parse multipart form")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "missing file field")
			return
		}
		defer file.Close()

		alt := r.FormValue("alt")

		rawBytes, err := io.ReadAll(file)
		if err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "could not read uploaded file")
			return
		}

		sniffed, bodyReader, err := sniffContentType(bytes.NewReader(rawBytes))
		if err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "could not read uploaded file")
			return
		}

		safeName := sanitiseKey(header.Filename)
		key := uuid.NewString() + "/" + safeName

		if err := adapter.Upload(ctx, UploadInput{
			Key:         key,
			Body:        bodyReader,
			Size:        header.Size,
			ContentType: sniffed,
		}); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "upload failed")
			return
		}

		record := &dbmodels.Media{
			ID:         uuid.NewString(),
			Filename:   header.Filename,
			StorageKey: key,
			MimeType:   sniffed,
			Size:       header.Size,
			Alt:        alt,
			CreatedAt:  time.Now().UTC(),
		}

		if claims, ok := auth.ClaimsFromContext(ctx); ok && claims != nil {
			record.UserID = claims.UserID
		}

		if proc != nil && strings.HasPrefix(sniffed, "image/") {
			variants, origWidth, origHeight, procErr := proc.ProcessImage(ctx, rawBytes, sniffed)
			if procErr != nil {
				slog.Warn("reverb: storage: image processing failed", "key", key, "err", procErr)
			} else if len(variants) > 0 {
				stored := make(map[string]StoredVariant, len(variants))
				for _, v := range variants {
					variantKey := key + "__" + v.Name
					uploadErr := adapter.Upload(ctx, UploadInput{
						Key:         variantKey,
						Body:        bytes.NewReader(v.Data),
						Size:        int64(len(v.Data)),
						ContentType: v.ContentType,
					})
					if uploadErr != nil {
						slog.Warn("reverb: storage: upload variant failed", "key", variantKey, "err", uploadErr)
						continue
					}
					stored[v.Name] = StoredVariant{
						Key:    variantKey,
						Width:  v.Width,
						Height: v.Height,
					}
				}

				if len(stored) > 0 {
					variantsJSON, marshalErr := json.Marshal(stored)
					if marshalErr == nil {
						record.Variants = variantsJSON
					}
				}

				record.Width = origWidth
				record.Height = origHeight
			} else if origWidth > 0 {
				record.Width = origWidth
				record.Height = origHeight
			}
		}

		if _, err := db.NewInsert().Model(record).Exec(ctx); err != nil {
			_ = adapter.Delete(ctx, key)
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not save record")
			return
		}

		api.JSON(w, http.StatusCreated, toResponse(record, adapter))
	}
}

// ---------------------------------------------------------------------------
// HandleDelete — DELETE /_reverb/storage/{id}
// ---------------------------------------------------------------------------

func HandleDelete(db *bun.DB, adapter Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		id := r.PathValue("id")

		record := new(dbmodels.Media)
		if err := db.NewSelect().Model(record).Where("id = ?", id).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				api.Error(w, http.StatusNotFound, api.CodeNotFound, "media not found")
			} else {
				api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not load record")
			}
			return
		}

		if claims, hasClaims := auth.ClaimsFromContext(r.Context()); hasClaims {
			isAdmin := roles.Level[claims.Role] >= roles.Level["admin"]
			isOwner := record.UserID == claims.UserID

			if !isAdmin && !isOwner {
				api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient permissions")
				return
			}
		}

		if _, err := db.NewDelete().Model(record).Where("id = ?", id).Exec(ctx); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "could not delete record")
			return
		}

		if err := adapter.Delete(ctx, record.StorageKey); err != nil {
			slog.Warn("reverb: storage: orphaned key after delete", "key", record.StorageKey, "err", err)
		}

		if len(record.Variants) > 0 {
			var stored map[string]StoredVariant
			if jsonErr := json.Unmarshal(record.Variants, &stored); jsonErr == nil {
				for _, sv := range stored {
					if delErr := adapter.Delete(ctx, sv.Key); delErr != nil {
						slog.Warn("reverb: storage: orphaned variant key after delete", "key", sv.Key, "err", delErr)
					}
				}
			}
		}

		api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ---------------------------------------------------------------------------
// HandleList — GET /_reverb/storage
// ---------------------------------------------------------------------------

func HandleList(db *bun.DB, adapter Adapter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		page, _ := strconv.Atoi(q.Get("page"))
		limit, _ := strconv.Atoi(q.Get("limit"))

		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 100 {
			limit = 20
		}

		claims, hasClaims := auth.ClaimsFromContext(r.Context())

		var records []dbmodels.Media
		baseQ := db.NewSelect().Model(&records).OrderExpr("created_at DESC")

		if hasClaims && roles.Level[claims.Role] >= roles.Level["admin"] {
		} else if hasClaims {
			baseQ = baseQ.Where("user_id = ?", claims.UserID)
		}

		total, err := baseQ.
			Limit(limit).
			Offset((page - 1) * limit).
			ScanAndCount(r.Context())
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		data := make([]mediaResponse, 0, len(records))
		for i := range records {
			data = append(data, toResponse(&records[i], adapter))
		}

		api.JSON(w, http.StatusOK, map[string]any{
			"data":  data,
			"total": total,
			"page":  page,
			"limit": limit,
		})
	}
}

func sniffContentType(file io.Reader) (string, io.Reader, error) {
	buf := make([]byte, 512)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", nil, err
	}
	buf = buf[:n]
	contentType := http.DetectContentType(buf)
	return contentType, io.MultiReader(bytes.NewReader(buf), file), nil
}
