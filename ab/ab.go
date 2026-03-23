package ab

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type Variant struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	WeightPercent int    `json:"weight_percent"`
}

// ---------------------------------------------------------------------------
// Assignment
// ---------------------------------------------------------------------------

func AssignVariant(ctx context.Context, db *bun.DB, testSlug, visitorID string) (string, error) {
	var existing dbmodels.ABTestAssignment
	err := db.NewSelect().
		Model(&existing).
		Where("test_slug = ?", testSlug).
		Where("visitor_id = ?", visitorID).
		Limit(1).
		Scan(ctx)
	if err == nil {
		return existing.VariantID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("ab: get assignment: %w", err)
	}

	var test dbmodels.ABTest
	if err := db.NewSelect().
		Model(&test).
		Where("slug = ?", testSlug).
		Where("active = ?", true).
		Limit(1).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("ab: test not found or inactive: %s", testSlug)
		}
		return "", fmt.Errorf("ab: get test: %w", err)
	}

	var variants []Variant
	if err := json.Unmarshal(test.Variants, &variants); err != nil {
		return "", fmt.Errorf("ab: parse variants: %w", err)
	}
	if len(variants) == 0 {
		return "", fmt.Errorf("ab: test has no variants")
	}

	variantID := pickVariant(testSlug, visitorID, variants)

	assignment := &dbmodels.ABTestAssignment{
		ID:        uuid.New().String(),
		TestSlug:  testSlug,
		VisitorID: visitorID,
		VariantID: variantID,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(assignment).Ignore().Exec(ctx); err != nil {
		return "", fmt.Errorf("ab: insert assignment: %w", err)
	}
	return variantID, nil
}

func pickVariant(testSlug, visitorID string, variants []Variant) string {
	h := sha256.Sum256([]byte(testSlug + "|" + visitorID))
	n := binary.BigEndian.Uint64(h[:8]) % 100

	var cumulative uint64
	for _, v := range variants {
		cumulative += uint64(v.WeightPercent)
		if n < cumulative {
			return v.ID
		}
	}
	return variants[len(variants)-1].ID
}

func RecordConversion(ctx context.Context, db *bun.DB, testSlug, visitorID, eventName string) error {
	ev := &dbmodels.ABConversionEvent{
		ID:        uuid.New().String(),
		TestSlug:  testSlug,
		VisitorID: visitorID,
		EventName: eventName,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(ev).Exec(ctx); err != nil {
		return fmt.Errorf("ab: record conversion: %w", err)
	}
	return nil
}
