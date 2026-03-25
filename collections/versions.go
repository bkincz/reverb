package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Snapshot
// ---------------------------------------------------------------------------

func SnapshotEntry(ctx context.Context, db bun.IDB, e dbmodels.CollectionEntry, createdByID string) error {
	var nextVersion int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM reverb_versions WHERE collection_slug = ? AND entry_id = ?`,
		e.CollectionSlug, e.ID,
	).Scan(&nextVersion); err != nil {
		return fmt.Errorf("collections: version number: %w", err)
	}

	v := &dbmodels.EntryVersion{
		ID:             uuid.New().String(),
		CollectionSlug: e.CollectionSlug,
		EntryID:        e.ID,
		Version:        nextVersion,
		Data:           e.Data,
		Status:         e.Status,
		CreatedByID:    createdByID,
		CreatedAt:      time.Now().UTC(),
	}

	if _, err := db.NewInsert().Model(v).Exec(ctx); err != nil {
		return fmt.Errorf("collections: insert version: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// List versions
// ---------------------------------------------------------------------------

func ListVersions(ctx context.Context, db *bun.DB, collectionSlug, entryID string, page, limit int) ([]map[string]any, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	q := db.NewSelect().
		Model((*dbmodels.EntryVersion)(nil)).
		Where("collection_slug = ?", collectionSlug).
		Where("entry_id = ?", entryID).
		OrderExpr("version DESC")

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("collections: count versions: %w", err)
	}

	var rows []dbmodels.EntryVersion
	if err := q.
		Column("id", "version", "status", "created_by", "label", "created_at").
		Limit(limit).
		Offset((page-1)*limit).
		Scan(ctx, &rows); err != nil {
		return nil, 0, fmt.Errorf("collections: list versions: %w", err)
	}

	out := make([]map[string]any, 0, len(rows))
	for _, v := range rows {
		out = append(out, map[string]any{
			"id":         v.ID,
			"version":    v.Version,
			"status":     v.Status,
			"created_by": v.CreatedByID,
			"label":      v.Label,
			"created_at": v.CreatedAt,
		})
	}

	return out, total, nil
}

// ---------------------------------------------------------------------------
// Get version
// ---------------------------------------------------------------------------

func GetVersion(ctx context.Context, db *bun.DB, collectionSlug, entryID string, version int) (map[string]any, error) {
	var v dbmodels.EntryVersion
	err := db.NewSelect().
		Model(&v).
		Where("collection_slug = ?", collectionSlug).
		Where("entry_id = ?", entryID).
		Where("version = ?", version).
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("collections: get version: %w", err)
	}

	var data map[string]any
	if err := json.Unmarshal(v.Data, &data); err != nil {
		return nil, fmt.Errorf("collections: unmarshal version data: %w", err)
	}

	return map[string]any{
		"id":         v.ID,
		"version":    v.Version,
		"status":     v.Status,
		"created_by": v.CreatedByID,
		"label":      v.Label,
		"created_at": v.CreatedAt,
		"data":       data,
	}, nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func HandleListVersions(db *bun.DB, reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if _, ok := reg.Get(slug); !ok {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "collection not found")
			return
		}

		id := r.PathValue("id")
		q := r.URL.Query()

		page, _ := strconv.Atoi(q.Get("page"))
		limit, _ := strconv.Atoi(q.Get("limit"))

		versions, total, err := ListVersions(r.Context(), db, slug, id, page, limit)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 100 {
			limit = 20
		}

		api.JSON(w, http.StatusOK, map[string]any{
			"data":  versions,
			"total": total,
			"page":  page,
			"limit": limit,
		})
	})
}

func HandleGetVersion(db *bun.DB, reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if _, ok := reg.Get(slug); !ok {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "collection not found")
			return
		}

		id := r.PathValue("id")
		versionStr := r.PathValue("version")
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "version must be an integer")
			return
		}

		v, err := GetVersion(r.Context(), db, slug, id, version)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		if v == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "version not found")
			return
		}

		api.JSON(w, http.StatusOK, v)
	})
}
