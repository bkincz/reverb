package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Stored schema shape
// ---------------------------------------------------------------------------

type storedField struct {
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required,omitempty"`
	Options  []string  `json:"options,omitempty"`
	MinRole  string    `json:"min_role,omitempty"`
}

// ---------------------------------------------------------------------------
// CheckDeprecations
// ---------------------------------------------------------------------------

func CheckDeprecations(ctx context.Context, db *bun.DB, reg *Registry, log *slog.Logger) error {
	for _, e := range reg.All() {
		if err := checkCollection(ctx, db, e, log); err != nil {
			return fmt.Errorf("collections: check deprecations for %q: %w", e.slug, err)
		}
	}
	return nil
}

func checkCollection(ctx context.Context, db *bun.DB, e Entry, log *slog.Logger) error {
	liveFields := make([]storedField, 0, len(e.schema.Fields))
	liveIndex := map[string]struct{}{}
	for _, f := range e.schema.Fields {
		liveFields = append(liveFields, storedField{
			Name:     f.Name,
			Type:     f.Type,
			Required: f.Required,
			Options:  f.Options,
			MinRole:  f.Access.RequiredRole(),
		})
		liveIndex[f.Name] = struct{}{}
	}

	liveSchemaJSON, err := json.Marshal(liveFields)
	if err != nil {
		return fmt.Errorf("marshal live schema: %w", err)
	}

	var existing dbmodels.Collection
	scanErr := db.NewSelect().
		Model(&existing).
		Where("slug = ?", e.slug).
		Limit(1).
		Scan(ctx)

	var deprecated []string

	if scanErr == nil {
		var stored []storedField
		if jsonErr := json.Unmarshal(existing.Schema, &stored); jsonErr == nil {
			for _, sf := range stored {
				if _, ok := liveIndex[sf.Name]; !ok {
					deprecated = append(deprecated, sf.Name)
				}
			}
		}

		var prevDeprecated []string
		if existing.DeprecatedFields != nil {
			_ = json.Unmarshal(existing.DeprecatedFields, &prevDeprecated)
		}
		deprecated = mergeUnique(prevDeprecated, deprecated)
	}

	if len(deprecated) > 0 {
		log.Warn("deprecated fields detected",
			"collection", e.slug,
			"fields", deprecated,
		)
	}

	deprecatedJSON, err := json.Marshal(deprecated)
	if err != nil {
		return fmt.Errorf("marshal deprecated fields: %w", err)
	}

	now := time.Now().UTC()

	row := &dbmodels.Collection{
		Slug:             e.slug,
		Name:             e.slug,
		Schema:           liveSchemaJSON,
		DeprecatedFields: deprecatedJSON,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if errors.Is(scanErr, sql.ErrNoRows) {
		row.ID = uuid.New().String()
		if _, insertErr := db.NewInsert().Model(row).Exec(ctx); insertErr != nil {
			return fmt.Errorf("insert collection row: %w", insertErr)
		}
	} else if scanErr == nil {
		row.ID = existing.ID
		row.Version = existing.Version + 1
		row.CreatedAt = existing.CreatedAt
		if _, updateErr := db.NewUpdate().Model(row).WherePK().Exec(ctx); updateErr != nil {
			return fmt.Errorf("update collection row: %w", updateErr)
		}
	} else {
		return fmt.Errorf("query collection row: %w", scanErr)
	}

	return nil
}

// ---------------------------------------------------------------------------
// WarnDeprecations
// ---------------------------------------------------------------------------

func WarnDeprecations(ctx context.Context, db *bun.DB, log *slog.Logger) {
	var cols []dbmodels.Collection
	if err := db.NewSelect().Model(&cols).Scan(ctx); err != nil {
		return
	}
	for _, c := range cols {
		if c.DeprecatedFields == nil {
			continue
		}
		var fields []string
		if err := json.Unmarshal(c.DeprecatedFields, &fields); err != nil || len(fields) == 0 {
			continue
		}
		log.Warn("deprecated fields detected",
			"collection", c.Slug,
			"fields", fields,
		)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mergeUnique(base, extra []string) []string {
	seen := map[string]struct{}{}
	for _, s := range base {
		seen[s] = struct{}{}
	}
	out := append([]string(nil), base...)
	for _, s := range extra {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
