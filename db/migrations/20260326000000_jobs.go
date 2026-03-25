package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"

	"github.com/bkincz/reverb/db/models"
)

func init() {
	Migrations.MustRegister(upJobs, downJobs)
}

func upJobs(ctx context.Context, bunDB *bun.DB) error {
	if _, err := bunDB.NewCreateTable().Model((*models.Job)(nil)).IfNotExists().Exec(ctx); err != nil {
		return fmt.Errorf("reverb: create table reverb_jobs: %w", err)
	}

	// ix_jobs_pending is the critical poll index — workers filter by status and
	// sort by run_at, so this composite index covers the full query plan.
	if _, err := bunDB.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS ix_jobs_pending ON reverb_jobs (status, run_at)`); err != nil {
		return fmt.Errorf("reverb: create index ix_jobs_pending: %w", err)
	}

	return nil
}

func downJobs(ctx context.Context, bunDB *bun.DB) error {
	isMySQL := bunDB.Dialect().Name() == dialect.MySQL

	var dropIdx string
	if isMySQL {
		dropIdx = "DROP INDEX ix_jobs_pending ON reverb_jobs"
	} else {
		dropIdx = "DROP INDEX IF EXISTS ix_jobs_pending"
	}
	if _, err := bunDB.ExecContext(ctx, dropIdx); err != nil {
		return fmt.Errorf("reverb: drop index ix_jobs_pending: %w", err)
	}

	if _, err := bunDB.NewDropTable().Model((*models.Job)(nil)).IfExists().Exec(ctx); err != nil {
		return fmt.Errorf("reverb: drop table reverb_jobs: %w", err)
	}

	return nil
}
