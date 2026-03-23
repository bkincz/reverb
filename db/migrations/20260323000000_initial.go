package migrations

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/bkincz/reverb/db/models"
)

func init() {
	Migrations.MustRegister(upInitial, downInitial)
}

func upInitial(ctx context.Context, bunDB *bun.DB) error {
	dbModels := []any{
		(*models.User)(nil),
		(*models.RefreshToken)(nil),
		(*models.Collection)(nil),
		(*models.CollectionEntry)(nil),
		(*models.CollectionSlug)(nil),
		(*models.Media)(nil),
		(*models.ABTest)(nil),
		(*models.ABTestAssignment)(nil),
		(*models.ABConversionEvent)(nil),
		(*models.FormDefinition)(nil),
		(*models.FormSubmission)(nil),
	}
	for _, model := range dbModels {
		if _, err := bunDB.NewCreateTable().Model(model).IfNotExists().Exec(ctx); err != nil {
			return fmt.Errorf("create table %T: %w", model, err)
		}
	}

	queries := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uix_collection_slugs ON reverb_collection_slugs (collection_slug, slug)`,
	}
	for _, q := range queries {
		if _, err := bunDB.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}

	return nil
}

func downInitial(ctx context.Context, bunDB *bun.DB) error {
	dbModels := []any{
		(*models.FormSubmission)(nil),
		(*models.FormDefinition)(nil),
		(*models.ABConversionEvent)(nil),
		(*models.ABTestAssignment)(nil),
		(*models.ABTest)(nil),
		(*models.Media)(nil),
		(*models.CollectionSlug)(nil),
		(*models.CollectionEntry)(nil),
		(*models.Collection)(nil),
		(*models.RefreshToken)(nil),
		(*models.User)(nil),
	}
	for _, model := range dbModels {
		if _, err := bunDB.NewDropTable().Model(model).IfExists().Exec(ctx); err != nil {
			return fmt.Errorf("drop table %T: %w", model, err)
		}
	}
	return nil
}
