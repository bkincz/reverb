package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"golang.org/x/crypto/bcrypt"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Read options
// ---------------------------------------------------------------------------

type ReadOptions struct {
	Locale   string
	Populate []string
	Reg      *Registry
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

var ErrEntryNotFound = errors.New("collections: entry not found")

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
	opts ReadOptions,
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
		row, err := entryToMap(ctx, db, e, role, schema, opts)
		if err != nil {
			return nil, 0, fmt.Errorf("collections: map entry %s: %w", e.ID, err)
		}
		out = append(out, row)
	}

	if len(opts.Populate) > 0 {
		if err := resolveJoins(ctx, db, out, schema, role, opts); err != nil {
			return nil, 0, fmt.Errorf("collections: resolve joins: %w", err)
		}
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
	opts ReadOptions,
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
	return entryToMap(ctx, db, e, role, schema, opts)
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

	var err error
	data, err = preprocessWrite(data, schema)
	if err != nil {
		return nil, fmt.Errorf("collections: preprocess write: %w", err)
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
			_, _ = upsertSlug(ctx, db, collectionSlug, e.ID, generateSlug(title))
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
	createdByID string,
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

	oldData := make(json.RawMessage, len(e.Data))
	copy(oldData, e.Data)
	oldStatus := e.Status

	existing := map[string]any{}
	if err := json.Unmarshal(e.Data, &existing); err != nil {
		return nil, fmt.Errorf("collections: unmarshal existing data: %w", err)
	}

	patch = stripUnknownFields(patch, schema)
	patch = stripUnwritableFields(patch, role, schema)

	if errs := ValidateData(patch, schema, false); len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}

	patch, err = preprocessWrite(patch, schema)
	if err != nil {
		return nil, fmt.Errorf("collections: preprocess write: %w", err)
	}

	maps.Copy(existing, patch)

	raw, err := json.Marshal(existing)
	if err != nil {
		return nil, fmt.Errorf("collections: marshal merged data: %w", err)
	}

	e.Data = raw
	e.UpdatedAt = time.Now().UTC()

	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if schema.Versioned {
			snap := e
			snap.Data = oldData
			snap.Status = oldStatus
			if err := SnapshotEntry(ctx, tx, snap, createdByID); err != nil {
				return fmt.Errorf("collections: snapshot: %w", err)
			}
		}
		if _, err := tx.NewUpdate().Model(&e).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("collections: update entry: %w", err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	if schema.SlugSource != "" {
		if title, ok := existing[schema.SlugSource].(string); ok && title != "" {
			_, _ = upsertSlug(ctx, db, collectionSlug, e.ID, generateSlug(title))
		}
	}

	return &e, nil
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func DeleteEntry(ctx context.Context, db *bun.DB, collectionSlug, id string) error {
	res, err := db.NewDelete().
		Model((*dbmodels.CollectionEntry)(nil)).
		Where("collection_slug = ?", collectionSlug).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("collections: delete entry: %w", err)
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEntryNotFound
	}

	_ = deleteSlug(ctx, db, id)

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

func jsonExtractExpr(db *bun.DB, col, field string) string {
	switch db.Dialect().Name() {
	case dialect.PG:
		return col + "->>" + "'" + field + "'"
	case dialect.MySQL:
		return col + "->>" + "'$." + field + "'"
	default:
		return "json_extract(" + col + ", '$." + field + "')"
	}
}

func entryToMap(ctx context.Context, db *bun.DB, e dbmodels.CollectionEntry, role string, schema Schema, opts ReadOptions) (map[string]any, error) {
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err != nil {
		return nil, fmt.Errorf("collections: unmarshal entry data: %w", err)
	}

	for _, f := range schema.Fields {
		if f.Type == TypeJoin {
			delete(data, f.Name)
			continue
		}
		rule := f.Access
		if rule == nil {
			rule = schema.Access.Read
		}
		if !rule.Allowed(role) {
			delete(data, f.Name)
		}
	}

	for _, f := range schema.Fields {
		if f.Type == TypePassword {
			delete(data, f.Name)
		}
	}

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

	for _, f := range schema.Fields {
		if f.Type != TypeLocale {
			continue
		}
		v, ok := data[f.Name]
		if !ok {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if role == "admin" || role == "editor" {
			data[f.Name+"_locales"] = m
		}
		if opts.Locale != "" {
			data[f.Name] = resolveLocale(m, opts.Locale)
		}
	}

	for _, f := range schema.Fields {
		if f.Type != TypeJoin {
			continue
		}
		if !contains(opts.Populate, f.Name) {
			continue
		}
		related, err := fetchJoinedEntries(ctx, db, opts.Reg, role, f.TargetSlug, f.JoinField, e.ID)
		if err != nil {
			return nil, fmt.Errorf("collections: populate join %q: %w", f.Name, err)
		}
		data[f.Name] = related
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
		if f.Type == TypeJoin {
			continue
		}
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

func preprocessWrite(data map[string]any, schema Schema) (map[string]any, error) {
	for _, f := range schema.Fields {
		if f.Type != TypePassword {
			continue
		}
		v, ok := data[f.Name]
		if !ok {
			continue
		}
		plain, ok := v.(string)
		if !ok || plain == "" {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(plain), 12)
		if err != nil {
			return nil, fmt.Errorf("hash password field %q: %w", f.Name, err)
		}
		data[f.Name] = string(hash)
	}
	return data, nil
}

func resolveJoins(ctx context.Context, db *bun.DB, entries []map[string]any, schema Schema, role string, opts ReadOptions) error {
	for _, f := range schema.Fields {
		if f.Type != TypeJoin {
			continue
		}
		if !contains(opts.Populate, f.Name) {
			continue
		}

		ids := make([]string, 0, len(entries))
		idIndex := map[string]int{}
		for i, row := range entries {
			id, _ := row["id"].(string)
			if id != "" {
				ids = append(ids, id)
				idIndex[id] = i
			}
		}
		if len(ids) == 0 {
			continue
		}

		related, err := fetchJoinedEntriesBatch(ctx, db, opts.Reg, role, f.TargetSlug, f.JoinField, ids)
		if err != nil {
			return fmt.Errorf("populate join %q: %w", f.Name, err)
		}

		grouped := map[string][]map[string]any{}
		for _, rel := range related {
			relData, _ := rel["data"].(map[string]any)
			if relData == nil {
				continue
			}
			parentID, _ := relData[f.JoinField].(string)
			grouped[parentID] = append(grouped[parentID], rel)
		}

		for _, row := range entries {
			parentID, _ := row["id"].(string)
			data, _ := row["data"].(map[string]any)
			if data == nil {
				data = map[string]any{}
				row["data"] = data
			}
			kids := grouped[parentID]
			if kids == nil {
				kids = []map[string]any{}
			}
			data[f.Name] = kids
		}
	}
	return nil
}

func fetchJoinedEntries(ctx context.Context, db *bun.DB, reg *Registry, role, targetSlug, joinField, parentID string) ([]map[string]any, error) {
	expr := jsonExtractExpr(db, "data", joinField)

	var rows []dbmodels.CollectionEntry
	err := db.NewSelect().
		Model(&rows).
		Where("collection_slug = ?", targetSlug).
		Where(expr+" = ?", parentID).
		Where("status != ?", "archived").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("collections: fetch joined entries: %w", err)
	}

	var targetSchema Schema
	if reg != nil {
		if te, ok := reg.Get(targetSlug); ok {
			targetSchema = te.schema
		}
	}

	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m, err := entryToMap(ctx, db, r, role, targetSchema, ReadOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func fetchJoinedEntriesBatch(ctx context.Context, db *bun.DB, reg *Registry, role, targetSlug, joinField string, parentIDs []string) ([]map[string]any, error) {
	expr := jsonExtractExpr(db, "data", joinField)

	ids := make([]any, len(parentIDs))
	for i, id := range parentIDs {
		ids[i] = id
	}

	var rows []dbmodels.CollectionEntry
	err := db.NewSelect().
		Model(&rows).
		Where("collection_slug = ?", targetSlug).
		Where(expr+" IN (?)", bun.List(ids)).
		Where("status != ?", "archived").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("collections: fetch joined entries batch: %w", err)
	}

	var targetSchema Schema
	if reg != nil {
		if te, ok := reg.Get(targetSlug); ok {
			targetSchema = te.schema
		}
	}

	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m, err := entryToMap(ctx, db, r, role, targetSchema, ReadOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func GetEntryRaw(ctx context.Context, db *bun.DB, collectionSlug, id string) (*dbmodels.CollectionEntry, error) {
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
		return nil, fmt.Errorf("collections: get entry raw: %w", err)
	}
	return &e, nil
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
