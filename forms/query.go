package forms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

func FindDefinition(ctx context.Context, db *bun.DB, slug string) (*dbmodels.FormDefinition, error) {
	var fd dbmodels.FormDefinition
	err := db.NewSelect().Model(&fd).Where("slug = ?", slug).Limit(1).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("forms: find definition: %w", err)
	}
	return &fd, nil
}

func SubmitForm(ctx context.Context, db *bun.DB, formID string, data map[string]any, schema Schema, metadata map[string]any) error {
	errs := validateSubmission(data, schema)
	if len(errs) > 0 {
		return &ValidationError{Fields: errs}
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("forms: marshal data: %w", err)
	}

	var rawMeta json.RawMessage
	if metadata != nil {
		rawMeta, err = json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("forms: marshal metadata: %w", err)
		}
	}

	sub := &dbmodels.FormSubmission{
		ID:        uuid.New().String(),
		FormID:    formID,
		Data:      raw,
		Metadata:  rawMeta,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(sub).Exec(ctx); err != nil {
		return fmt.Errorf("forms: insert submission: %w", err)
	}
	return nil
}

func ListSubmissions(ctx context.Context, db *bun.DB, formID string, page, limit int) ([]dbmodels.FormSubmission, int, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	q := db.NewSelect().Model((*dbmodels.FormSubmission)(nil)).Where("form_id = ?", formID)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("forms: count submissions: %w", err)
	}

	var subs []dbmodels.FormSubmission
	if err := q.OrderExpr("created_at DESC").Limit(limit).Offset((page-1)*limit).Scan(ctx, &subs); err != nil {
		return nil, 0, fmt.Errorf("forms: list submissions: %w", err)
	}
	return subs, total, nil
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

func validateSubmission(data map[string]any, schema Schema) map[string]string {
	errs := map[string]string{}
	for _, f := range schema.Fields {
		v, ok := data[f.Name]
		if !ok || v == nil {
			if f.Required {
				errs[f.Name] = "this field is required"
			}
			continue
		}
		switch f.Type {
		case FieldTypeEmail:
			s, ok := v.(string)
			if !ok || !isEmailLike(s) {
				errs[f.Name] = "must be a valid email address"
			}
		case FieldTypeNumber:
			switch v.(type) {
			case float64, int, int64:
				// ok
			default:
				errs[f.Name] = "must be a number"
			}
		case FieldTypeBoolean:
			if _, ok := v.(bool); !ok {
				errs[f.Name] = "must be a boolean"
			}
		case FieldTypeSelect:
			s, ok := v.(string)
			if !ok {
				errs[f.Name] = "must be a string"
				continue
			}
			if !containsStr(f.Options, s) {
				errs[f.Name] = "invalid option"
			}
		}
	}
	return errs
}

func isEmailLike(s string) bool {
	for _, c := range s {
		if c == '@' {
			return true
		}
	}
	return false
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("forms: validation failed: %v", e.Fields)
}
