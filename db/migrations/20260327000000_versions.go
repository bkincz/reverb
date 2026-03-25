package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"

	"github.com/bkincz/reverb/db/models"
)

func init() {
	Migrations.MustRegister(upVersions, downVersions)
}

func upVersions(ctx context.Context, bunDB *bun.DB) error {
	if _, err := bunDB.NewCreateTable().Model((*models.EntryVersion)(nil)).IfNotExists().Exec(ctx); err != nil {
		return fmt.Errorf("reverb: create table reverb_versions: %w", err)
	}

	// ix_ev_entry covers all version queries filtered by entry.
	if _, err := bunDB.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_ev_entry ON reverb_versions (collection_slug, entry_id, version DESC)`); err != nil {
		return fmt.Errorf("reverb: create index ix_ev_entry: %w", err)
	}

	return nil
}

func downVersions(ctx context.Context, bunDB *bun.DB) error {
	isMySQL := bunDB.Dialect().Name() == dialect.MySQL

	var dropIdx string
	if isMySQL {
		dropIdx = "DROP INDEX ix_ev_entry ON reverb_versions"
	} else {
		dropIdx = "DROP INDEX IF EXISTS ix_ev_entry"
	}
	if _, err := bunDB.ExecContext(ctx, dropIdx); err != nil {
		return fmt.Errorf("reverb: drop index ix_ev_entry: %w", err)
	}

	if _, err := bunDB.NewDropTable().Model((*models.EntryVersion)(nil)).IfExists().Exec(ctx); err != nil {
		return fmt.Errorf("reverb: drop table reverb_versions: %w", err)
	}

	return nil
}
