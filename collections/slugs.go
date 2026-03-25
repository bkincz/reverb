package collections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Slug helpers
// ---------------------------------------------------------------------------

var reSpaces = regexp.MustCompile(`[\s_-]+`)

func generateSlug(title string) string {
	s := strings.ToLower(title)
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' {
			return r
		}
		return -1
	}, s)
	s = reSpaces.ReplaceAllString(strings.TrimSpace(s), "-")
	if s == "" {
		s = "entry"
	}
	return s
}

func upsertSlug(ctx context.Context, db *bun.DB, collectionSlug, entryID, desired string) (string, error) {
	slug := desired
	for i := 0; i < 10; i++ {
		candidate := slug
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", slug, i)
		}

		row := &dbmodels.CollectionSlug{
			EntryID:        entryID,
			CollectionSlug: collectionSlug,
			Slug:           candidate,
		}
		_, err := db.NewInsert().
			Model(row).
			On("CONFLICT (entry_id) DO UPDATE SET slug = EXCLUDED.slug, collection_slug = EXCLUDED.collection_slug").
			Exec(ctx)
		if err == nil {
			return candidate, nil
		}
		if isUniqueViolation(err) {
			continue
		}
		return "", fmt.Errorf("collections: upsert slug: %w", err)
	}

	fallback := fmt.Sprintf("%s-%s", slug, uuid.New().String()[:8])
	row := &dbmodels.CollectionSlug{
		EntryID:        entryID,
		CollectionSlug: collectionSlug,
		Slug:           fallback,
	}
	if _, err := db.NewInsert().Model(row).
		On("CONFLICT (entry_id) DO UPDATE SET slug = EXCLUDED.slug, collection_slug = EXCLUDED.collection_slug").
		Exec(ctx); err != nil {
		return "", fmt.Errorf("collections: upsert slug fallback: %w", err)
	}
	return fallback, nil
}

func deleteSlug(ctx context.Context, db *bun.DB, entryID string) error {
	_, err := db.NewDelete().
		Model((*dbmodels.CollectionSlug)(nil)).
		Where("entry_id = ?", entryID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("collections: delete slug: %w", err)
	}
	return nil
}

func GetEntryBySlug(ctx context.Context, db *bun.DB, collectionSlug, entrySlug, role string, schema Schema, opts ReadOptions) (map[string]any, error) {
	var cs dbmodels.CollectionSlug
	err := db.NewSelect().
		Model(&cs).
		Where("collection_slug = ?", collectionSlug).
		Where("slug = ?", entrySlug).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("collections: get slug: %w", err)
	}
	return GetEntry(ctx, db, collectionSlug, cs.EntryID, role, schema, opts)
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "1062")
}
