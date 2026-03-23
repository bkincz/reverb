package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(upIndexes, downIndexes)
}

func upIndexes(ctx context.Context, bunDB *bun.DB) error {
	queries := []string{
		// collection_entries: covers all list queries with optional status filter
		// and the default created_at sort. The composite (slug, status, created_at)
		// lets the DB satisfy WHERE slug=? ORDER BY created_at as a prefix scan.
		`CREATE INDEX IF NOT EXISTS ix_ce_slug_status_created
		    ON reverb_collection_entries (collection_slug, status, created_at)`,

		// collection_entries: covers the scheduler query
		// WHERE status='draft' AND publish_at IS NOT NULL AND publish_at <= ?
		`CREATE INDEX IF NOT EXISTS ix_ce_status_publish_at
		    ON reverb_collection_entries (status, publish_at)`,

		// ab_assignments: covers the deterministic variant lookup
		// WHERE test_slug = ? AND visitor_id = ?
		`CREATE INDEX IF NOT EXISTS ix_ab_asgn_test_visitor
		    ON reverb_ab_assignments (test_slug, visitor_id)`,

		// ab_conversion_events: covers conversion queries filtered by test
		`CREATE INDEX IF NOT EXISTS ix_ab_conv_test_created
		    ON reverb_ab_conversion_events (test_slug, created_at)`,

		// form_submissions: covers paginated listing by form
		// WHERE form_id = ? ORDER BY created_at DESC
		`CREATE INDEX IF NOT EXISTS ix_form_sub_form_created
		    ON reverb_form_submissions (form_id, created_at)`,

		// media: covers the non-admin storage list
		// WHERE user_id = ? ORDER BY created_at DESC
		`CREATE INDEX IF NOT EXISTS ix_media_user_created
		    ON reverb_media (user_id, created_at)`,
	}

	for _, q := range queries {
		if _, err := bunDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("reverb: create index: %w", err)
		}
	}
	return nil
}

func downIndexes(ctx context.Context, bunDB *bun.DB) error {
	type spec struct {
		index string
		table string
	}
	specs := []spec{
		{"ix_ce_slug_status_created", "reverb_collection_entries"},
		{"ix_ce_status_publish_at", "reverb_collection_entries"},
		{"ix_ab_asgn_test_visitor", "reverb_ab_assignments"},
		{"ix_ab_conv_test_created", "reverb_ab_conversion_events"},
		{"ix_form_sub_form_created", "reverb_form_submissions"},
		{"ix_media_user_created", "reverb_media"},
	}

	// MySQL requires DROP INDEX name ON table; PostgreSQL and SQLite accept
	// DROP INDEX IF EXISTS name (no table qualifier needed).
	isMySQL := bunDB.Dialect().Name() == dialect.MySQL

	for _, s := range specs {
		var q string
		if isMySQL {
			q = fmt.Sprintf("DROP INDEX %s ON %s", s.index, s.table)
		} else {
			q = fmt.Sprintf("DROP INDEX IF EXISTS %s", s.index)
		}
		if _, err := bunDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("reverb: drop index %s: %w", s.index, err)
		}
	}
	return nil
}
