package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

type ListParams struct {
	Status string
	Page   int
	Limit  int
	Sort   string
}

func ListEntries(
	ctx context.Context,
	db *bun.DB,
	collectionSlug string,
	params ListParams,
	role string,
	schema Schema,
) ([]map[string]any, int, error) {
	if params.Page < 1 {
		params.Page = 1
	}
	if params.Limit < 1 || params.Limit > 100 {
		params.Limit = 20
	}

	q := db.NewSelect().
		Model((*dbmodels.CollectionEntry)(nil)).
		Where("collection_slug = ?", collectionSlug)

	if params.Status != "" {
		q = q.Where("status = ?", params.Status)
	}

	sortCol, sortDir := parseSortParam(params.Sort)
	q = q.OrderExpr("? ?", bun.Ident(sortCol), bun.Safe(sortDir))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	var entries []dbmodels.CollectionEntry
	err = q.Limit(params.Limit).Offset((params.Page-1)*params.Limit).Scan(ctx, &entries)
	if err != nil {
		return nil, 0, err
	}

	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row, err := entryToMap(e, role, schema)
		if err != nil {
			return nil, 0, fmt.Errorf("collections: map entry %s: %w", e.ID, err)
		}
		out = append(out, row)
	}
	return out, total, nil
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

func GetEntry(
	ctx context.Context,
	db *bun.DB,
	collectionSlug, id, role string,
	schema Schema,
) (map[string]any, error) {
	var e dbmodels.CollectionEntry
	err := db.NewSelect().
		Model(&e).
		Where("collection_slug = ?", collectionSlug).
		Where("id = ?", id).
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("collections: get entry: %w", err)
	}
	return entryToMap(e, role, schema)
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func CreateEntry(
	ctx context.Context,
	db *bun.DB,
	collectionSlug string,
	data map[string]any,
	status string,
	role string,
	schema Schema,
) (*dbmodels.CollectionEntry, error) {
	data = stripUnknownFields(data, schema)
	data = stripUnwritableFields(data, role, schema)

	if errs := ValidateData(data, schema, true); len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}

	if status == "" {
		status = "draft"
	} else if err := validateStatus(status); err != nil {
		return nil, &ValidationError{Fields: map[string]string{"status": err.Error()}}
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("collections: marshal data: %w", err)
	}

	now := time.Now().UTC()
	e := &dbmodels.CollectionEntry{
		ID:             uuid.New().String(),
		CollectionSlug: collectionSlug,
		Status:         status,
		Data:           raw,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if _, err := db.NewInsert().Model(e).Exec(ctx); err != nil {
		return nil, fmt.Errorf("collections: insert entry: %w", err)
	}

	if schema.SlugSource != "" {
		if title, ok := data[schema.SlugSource].(string); ok && title != "" {
			if _, err := upsertSlug(ctx, db, collectionSlug, e.ID, generateSlug(title)); err != nil {
				// Non-fatal: slug is a convenience feature, entry already persisted.
				_ = err
			}
		}
	}

	return e, nil
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func UpdateEntry(
	ctx context.Context,
	db *bun.DB,
	collectionSlug, id string,
	patch map[string]any,
	status string,
	publishAt *time.Time,
	role string,
	schema Schema,
) (*dbmodels.CollectionEntry, error) {
	var e dbmodels.CollectionEntry
	err := db.NewSelect().
		Model(&e).
		Where("collection_slug = ?", collectionSlug).
		Where("id = ?", id).
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("collections: get entry: %w", err)
	}

	if status != "" {
		if err := validateStatus(status); err != nil {
			return nil, &ValidationError{Fields: map[string]string{"status": err.Error()}}
		}
		e.Status = status
	}
	if publishAt != nil {
		e.PublishAt = publishAt
	}

	existing := map[string]any{}
	if err := json.Unmarshal(e.Data, &existing); err != nil {
		return nil, fmt.Errorf("collections: unmarshal existing data: %w", err)
	}

	patch = stripUnknownFields(patch, schema)
	patch = stripUnwritableFields(patch, role, schema)

	if errs := ValidateData(patch, schema, false); len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}

	for k, v := range patch {
		existing[k] = v
	}

	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("collections: marshal merged data: %w", err)
	}

	e.Data = raw
	e.UpdatedAt = time.Now().UTC()

	if _, err := db.NewUpdate().Model(&e).WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("collections: update entry: %w", err)
	}

	if schema.SlugSource != "" {
		if title, ok := existing[schema.SlugSource].(string); ok && title != "" {
			if _, err := upsertSlug(ctx, db, collectionSlug, e.ID, generateSlug(title)); err != nil {
				// Non-fatal: slug is a convenience feature, entry already persisted.
				_ = err
			}
		}
	}

	return &e, nil
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func DeleteEntry(ctx context.Context, db *bun.DB, collectionSlug, id string) error {
	_, err := db.NewDelete().
		Model((*dbmodels.CollectionEntry)(nil)).
		Where("collection_slug = ?", collectionSlug).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("collections: delete entry: %w", err)
	}

	if err := deleteSlug(ctx, db, id); err != nil {
		// Non-fatal: entry is already deleted.
		_ = err
	}

	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func parseSortParam(sort string) (col, dir string) {
	col, dir = "created_at", "DESC"
	if sort == "" {
		return
	}
	parts := strings.SplitN(sort, ":", 2)
	candidate := strings.ToLower(parts[0])
	if candidate != "created_at" && candidate != "updated_at" {
		return
	}
	col = candidate
	if len(parts) == 2 {
		switch strings.ToUpper(parts[1]) {
		case "ASC":
			dir = "ASC"
		case "DESC":
			dir = "DESC"
		}
	}
	return
}

func entryToMap(e dbmodels.CollectionEntry, role string, schema Schema) (map[string]any, error) {
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("collections: unmarshal entry data: %w", err)
	}

	// Strip fields the role cannot read.
	for _, f := range schema.Fields {
		rule := f.Access
		if rule == nil {
			rule = schema.Access.Read
		}
		if !rule.Allowed(role) {
			delete(data, f.Name)
		}
	}

	// Render RichText fields to HTML and expose alongside the raw JSON.
	for _, f := range schema.Fields {
		if f.Type != TypeRichText {
			continue
		}
		if v, ok := data[f.Name]; ok {
			if doc, ok := v.(map[string]any); ok {
				data[f.Name+"_html"] = RenderProseMirror(doc)
			}
		}
	}

	return map[string]any{
		"id":         e.ID,
		"status":     e.Status,
		"created_at": e.CreatedAt,
		"updated_at": e.UpdatedAt,
		"data":       data,
	}, nil
}

func EntryToMapFull(e dbmodels.CollectionEntry) (map[string]any, error) {
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("collections: unmarshal entry data: %w", err)
	}
	return map[string]any{
		"id":         e.ID,
		"status":     e.Status,
		"created_at": e.CreatedAt,
		"updated_at": e.UpdatedAt,
		"data":       data,
	}, nil
}

func validateStatus(s string) error {
	switch s {
	case "draft", "published", "archived":
		return nil
	}
	return fmt.Errorf("must be one of: draft, published, archived")
}

func stripUnknownFields(data map[string]any, schema Schema) map[string]any {
	known := map[string]struct{}{}
	for _, f := range schema.Fields {
		known[f.Name] = struct{}{}
	}
	out := make(map[string]any, len(data))
	for k, v := range data {
		if _, ok := known[k]; ok {
			out[k] = v
		}
	}
	return out
}

func stripUnwritableFields(data map[string]any, role string, schema Schema) map[string]any {
	out := make(map[string]any, len(data))
	for _, f := range schema.Fields {
		rule := f.Access
		if rule == nil {
			rule = schema.Access.Write
		}
		if v, ok := data[f.Name]; ok && rule.Allowed(role) {
			out[f.Name] = v
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("collections: validation failed: %v", e.Fields)
}

func IsValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}
