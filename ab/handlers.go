package ab

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/api"
	"github.com/bkincz/reverb/auth"
	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func abError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTestNotFound) {
		api.Error(w, http.StatusNotFound, api.CodeNotFound, "test not found or inactive")
		return
	}
	api.Error(w, http.StatusInternalServerError, api.CodeInternalError, "internal error")
}

func validateABTestPayload(raw json.RawMessage) error {
	if _, err := ParseVariants(raw); err != nil {
		return err
	}
	return nil
}

func HandleAssign(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		visitorID := r.URL.Query().Get("visitor_id")
		if visitorID == "" {
			if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
				visitorID = claims.UserID
			}
		}
		if visitorID == "" {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "visitor_id is required")
			return
		}

		variantID, err := AssignVariant(r.Context(), db, slug, visitorID)
		if err != nil {
			abError(w, err)
			return
		}

		api.JSON(w, http.StatusOK, map[string]string{"variant_id": variantID})
	}
}

func HandleConvert(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		var body struct {
			VisitorID string `json:"visitor_id"`
			EventName string `json:"event_name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		if body.VisitorID == "" {
			if claims, ok := auth.ClaimsFromContext(r.Context()); ok && claims != nil {
				body.VisitorID = claims.UserID
			}
		}
		if body.VisitorID == "" || body.EventName == "" {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "visitor_id and event_name are required")
			return
		}

		if err := RecordConversion(r.Context(), db, slug, body.VisitorID, body.EventName); err != nil {
			abError(w, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func HandleAdminList(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var tests []dbmodels.ABTest
		if err := db.NewSelect().Model(&tests).OrderExpr("created_at DESC").Scan(r.Context()); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}
		api.JSON(w, http.StatusOK, map[string]any{"data": tests})
	}
}

func HandleAdminCreate(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body dbmodels.ABTest
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}
		if body.Slug == "" || body.Name == "" {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "slug and name are required")
			return
		}
		if err := validateABTestPayload(body.Variants); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, err.Error())
			return
		}
		body.ID = uuid.New().String()
		body.CreatedAt = time.Now().UTC()

		if _, err := db.NewInsert().Model(&body).Exec(r.Context()); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusCreated, body)
	}
}

func HandleAdminGet(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		var test dbmodels.ABTest
		err := db.NewSelect().Model(&test).Where("slug = ?", slug).Limit(1).Scan(r.Context())
		if errors.Is(err, sql.ErrNoRows) {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "test not found")
			return
		}
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusOK, test)
	}
}

func HandleAdminUpdate(db *bun.DB) http.HandlerFunc {
	type patchBody struct {
		Name     *string          `json:"name"`
		Active   *bool            `json:"active"`
		Variants *json.RawMessage `json:"variants"`
		Rules    *json.RawMessage `json:"rules"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		var patch patchBody
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			api.Error(w, http.StatusBadRequest, api.CodeValidationError, "invalid JSON body")
			return
		}

		var test dbmodels.ABTest
		err := db.NewSelect().Model(&test).Where("slug = ?", slug).Limit(1).Scan(r.Context())
		if errors.Is(err, sql.ErrNoRows) {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "test not found")
			return
		}
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		q := db.NewUpdate().Model(&test).Where("slug = ?", slug)

		if patch.Name != nil {
			test.Name = *patch.Name
			q = q.Set("name = ?", test.Name)
		}
		if patch.Active != nil {
			test.Active = *patch.Active
			q = q.Set("active = ?", test.Active)
		}
		if patch.Variants != nil {
			if err := validateABTestPayload(*patch.Variants); err != nil {
				api.Error(w, http.StatusBadRequest, api.CodeValidationError, err.Error())
				return
			}
			test.Variants = *patch.Variants
			q = q.Set("variants = ?", test.Variants)
		}
		if patch.Rules != nil {
			test.Rules = *patch.Rules
			q = q.Set("rules = ?", test.Rules)
		}

		if _, err := q.Exec(r.Context()); err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		api.JSON(w, http.StatusOK, test)
	}
}

func HandleAdminDelete(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")

		res, err := db.NewDelete().
			Model((*dbmodels.ABTest)(nil)).
			Where("slug = ?", slug).
			Exec(r.Context())
		if err != nil {
			api.Error(w, http.StatusInternalServerError, api.CodeInternalError, err.Error())
			return
		}

		n, _ := res.RowsAffected()
		if n == 0 {
			api.Error(w, http.StatusNotFound, api.CodeNotFound, "test not found")
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}
