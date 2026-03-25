package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

func init() {
	Migrations.MustRegister(upMediaVariants, downMediaVariants)
}

func upMediaVariants(ctx context.Context, bunDB *bun.DB) error {
	stmts := []string{
		`ALTER TABLE reverb_media ADD COLUMN width INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE reverb_media ADD COLUMN height INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE reverb_media ADD COLUMN variants JSON`,
	}
	for _, q := range stmts {
		if _, err := bunDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("reverb: media variants migration up: %w", err)
		}
	}
	return nil
}

func downMediaVariants(ctx context.Context, bunDB *bun.DB) error {
	stmts := []string{
		`ALTER TABLE reverb_media DROP COLUMN variants`,
		`ALTER TABLE reverb_media DROP COLUMN height`,
		`ALTER TABLE reverb_media DROP COLUMN width`,
	}
	for _, q := range stmts {
		if _, err := bunDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("reverb: media variants migration down: %w", err)
		}
	}
	return nil
}
