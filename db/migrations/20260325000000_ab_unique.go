package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
)

func init() {
	Migrations.MustRegister(upABUnique, downABUnique)
}

func upABUnique(ctx context.Context, bunDB *bun.DB) error {
	if _, err := bunDB.ExecContext(ctx,
		`CREATE UNIQUE INDEX IF NOT EXISTS uix_ab_asgn_test_visitor
		    ON reverb_ab_assignments (test_slug, visitor_id)`); err != nil {
		return fmt.Errorf("reverb: create unique index uix_ab_asgn_test_visitor: %w", err)
	}
	return nil
}

func downABUnique(ctx context.Context, bunDB *bun.DB) error {
	isMySQL := bunDB.Dialect().Name() == dialect.MySQL
	var q string
	if isMySQL {
		q = "DROP INDEX uix_ab_asgn_test_visitor ON reverb_ab_assignments"
	} else {
		q = "DROP INDEX IF EXISTS uix_ab_asgn_test_visitor"
	}
	if _, err := bunDB.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("reverb: drop index uix_ab_asgn_test_visitor: %w", err)
	}
	return nil
}
