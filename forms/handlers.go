package forms

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	dbmodels "github.com/bkincz/reverb/db/models"
	"github.com/bkincz/reverb/internal/realip"
)

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func HandleSubmit(db *bun.DB, reg *Registry, clientIPFns ...func(*http.Request) string) http.HandlerFunc {
	clientIP := realip.RemoteAddr
	if len(clientIPFns) > 0 && clientIPFns[0] != nil {
		clientIP = clientIPFns[0]
	}

	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		schema, ok := reg.Get(slug)
		if !ok {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "form not found")
			return
		}

		var data map[string]any
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		if schema.HoneypotField != "" && honeypotFilled(data[schema.HoneypotField]) {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		fd, err := FindDefinition(r.Context(), db, slug)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		if fd == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "form not found")
			return
		}

		metadata := map[string]any{
			"ip":         clientIP(r),
			"user_agent": r.Header.Get("User-Agent"),
		}

		if err := SubmitForm(r.Context(), db, fd.ID, data, schema, metadata); err != nil {
			if ve, ok := err.(*ValidationError); ok {
				api.FieldError(w, ve.Fields)
				return
			}
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func honeypotFilled(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	default:
		return true
	}
}

func HandleAdminListForms(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var forms []dbmodels.FormDefinition
		if err := db.NewSelect().Model(&forms).OrderExpr("created_at DESC").Scan(r.Context()); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		api.JSON(w, http.StatusOK, map[string]any{"data": forms})
	}
}

func HandleAdminListSubmissions(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		fd, err := FindDefinition(r.Context(), db, slug)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		if fd == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "form not found")
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

		subs, total, err := ListSubmissions(r.Context(), db, fd.ID, page, limit)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusOK, map[string]any{"data": subs, "total": total})
	}
}
