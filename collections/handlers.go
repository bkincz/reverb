package collections

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	"github.com/bkincz/reverb/internal/roles"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type PublishFunc func(slug, eventType string, entry map[string]any, id string)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func roleFromRequest(r *http.Request) string {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		return "public"
	}
	return claims.Role
}

func resolveCollection(w http.ResponseWriter, r *http.Request, reg *Registry) (string, Schema, bool) {
	slug := r.PathValue("slug")
	e, ok := reg.Get(slug)
	if !ok {
		api.Error(w, http.StatusNotFound, api.CodeNotFound, "collection not found")
		return "", Schema{}, false
	}
	return slug, e.schema, true
}

func handleValidationOrInternal(w http.ResponseWriter, err error) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		api.FieldError(w, ve.Fields)
		return
	}
	api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
}

func readOptionsFromRequest(r *http.Request) ReadOptions {
	q := r.URL.Query()

	locale := q.Get("locale")
	if locale == "" {
		if al := r.Header.Get("Accept-Language"); al != "" {
			tag := strings.SplitN(al, ",", 2)[0]
			if idx := strings.IndexByte(tag, ';'); idx >= 0 {
				tag = tag[:idx]
			}
			locale = strings.TrimSpace(tag)
		}
	}

	var populate []string
	if raw := q.Get("populate"); raw != "" {
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				populate = append(populate, p)
			}
		}
	}

	return ReadOptions{Locale: locale, Populate: populate}
}

// ---------------------------------------------------------------------------
// HandleList
// ---------------------------------------------------------------------------

func HandleList(db *bun.DB, reg *Registry, publish PublishFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, schema, ok := resolveCollection(w, r, reg)
		if !ok {
			return
		}

		role := roleFromRequest(r)
		if !schema.Access.Read.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		limit, _ := strconv.Atoi(q.Get("limit"))

		if page < 1 {
			page = 1
		}
		if limit < 1 || limit > 100 {
			limit = 20
		}

		params := ListParams{
			Status: q.Get("status"),
			Page:   page,
			Limit:  limit,
			Sort:   q.Get("sort"),
		}

		opts := readOptionsFromRequest(r)
		opts.Reg = reg
		entries, total, err := ListEntries(r.Context(), db, slug, params, role, schema, opts)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusOK, map[string]any{
			"data":  entries,
			"total": total,
			"page":  params.Page,
			"limit": params.Limit,
		})
	}
}

// ---------------------------------------------------------------------------
// HandleGet
// ---------------------------------------------------------------------------

func HandleGet(db *bun.DB, reg *Registry, publish PublishFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, schema, ok := resolveCollection(w, r, reg)
		if !ok {
			return
		}

		role := roleFromRequest(r)
		if !schema.Access.Read.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		opts := readOptionsFromRequest(r)
		opts.Reg = reg

		if entrySlug := r.URL.Query().Get("slug"); entrySlug != "" {
			entry, err := GetEntryBySlug(r.Context(), db, slug, entrySlug, role, schema, opts)
			if err != nil {
				api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
				return
			}
			if entry == nil {
				api.Error(w, http.StatusNotFound, api.CodeNotFound, "entry not found")
				return
			}
			api.JSON(w, http.StatusOK, entry)
			return
		}

		id := r.PathValue("id")
		entry, err := GetEntry(r.Context(), db, slug, id, role, schema, opts)
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		if entry == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "entry not found")
			return
		}

		api.JSON(w, http.StatusOK, entry)
	}
}

// ---------------------------------------------------------------------------
// HandleCreate
// ---------------------------------------------------------------------------

func HandleCreate(db *bun.DB, reg *Registry, publish PublishFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, schema, ok := resolveCollection(w, r, reg)
		if !ok {
			return
		}

		role := roleFromRequest(r)
		if !schema.Access.Write.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		var body struct {
			Status string         `json:"status"`
			Data   map[string]any `json:"data"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		data := body.Data
		if data == nil {
			data = map[string]any{}
		}

		entry, err := CreateEntry(r.Context(), db, slug, data, body.Status, role, schema)
		if err != nil {
			handleValidationOrInternal(w, err)
			return
		}

		if publish != nil {
			if full, mapErr := EntryToMapFull(*entry); mapErr == nil {
				publish(slug, "entry.created", full, entry.ID)
			}
		}

		filtered, mapErr := entryToMap(r.Context(), db, *entry, role, schema, ReadOptions{})
		if mapErr != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, mapErr.Error())
			return
		}
		api.JSON(w, http.StatusCreated, filtered)
	}
}

// ---------------------------------------------------------------------------
// HandleUpdate
// ---------------------------------------------------------------------------

func HandleUpdate(db *bun.DB, reg *Registry, publish PublishFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, schema, ok := resolveCollection(w, r, reg)
		if !ok {
			return
		}

		role := roleFromRequest(r)
		if !schema.Access.Write.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		id := r.PathValue("id")

		var body struct {
			Status    string         `json:"status"`
			PublishAt *time.Time     `json:"publish_at"`
			Data      map[string]any `json:"data"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		patch := body.Data
		if patch == nil {
			patch = map[string]any{}
		}

		var userID string
		if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
			userID = claims.UserID
		}

		result, err := UpdateEntry(r.Context(), db, slug, id, patch, body.Status, body.PublishAt, role, schema, userID)
		if err != nil {
			handleValidationOrInternal(w, err)
			return
		}
		if result == nil {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "entry not found")
			return
		}

		if publish != nil {
			if full, mapErr := EntryToMapFull(*result); mapErr == nil {
				publish(slug, "entry.updated", full, result.ID)
			}
		}

		filtered, mapErr := entryToMap(r.Context(), db, *result, role, schema, ReadOptions{})
		if mapErr != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, mapErr.Error())
			return
		}
		api.JSON(w, http.StatusOK, filtered)
	}
}

// ---------------------------------------------------------------------------
// HandleDelete
// ---------------------------------------------------------------------------

func HandleDelete(db *bun.DB, reg *Registry, publish PublishFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug, schema, ok := resolveCollection(w, r, reg)
		if !ok {
			return
		}

		role := roleFromRequest(r)
		if !schema.Access.Delete.Allowed(role) {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		id := r.PathValue("id")
		if err := DeleteEntry(r.Context(), db, slug, id); err != nil {
			if errors.Is(err, ErrEntryNotFound) {
				api.Error(w, http.StatusNotFound, api.CodeNotFound, "entry not found")
				return
			}
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		if publish != nil {
			publish(slug, "entry.deleted", nil, id)
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// ---------------------------------------------------------------------------
// HandleAdminList
// ---------------------------------------------------------------------------

func HandleAdminList(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := roleFromRequest(r)
		if !roles.Allowed(role, "admin") {
			api.Error(w, http.StatusForbidden, api.CodeForbidden, "insufficient role")
			return
		}

		all := reg.All()
		out := make([]map[string]any, 0, len(all))
		for _, e := range all {
			out = append(out, map[string]any{
				"slug":   e.slug,
				"schema": e.schema,
			})
		}

		api.JSON(w, http.StatusOK, map[string]any{"data": out})
	}
}
