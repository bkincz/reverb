package db

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	dbmigrations "github.com/bkincz/reverb/db/migrations"
)

func Migrate(ctx context.Context, db *bun.DB) error {
	migrator := migrate.NewMigrator(db, dbmigrations.Migrations)

	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("reverb: migration init: %w", err)
	}

	group, err := migrator.Migrate(ctx)
	if err != nil {
		return fmt.Errorf("reverb: migrate: %w", err)
	}
	_ = group

	return nil
}
