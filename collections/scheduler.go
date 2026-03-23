package collections

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/uptrace/bun"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Scheduler
// ---------------------------------------------------------------------------

func StartScheduler(ctx context.Context, db *bun.DB, log *slog.Logger, publish func(slug, typ string, entry map[string]any, id string)) {
	go func() {
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()

		runOnce(ctx, db, log, publish)

		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				runOnce(ctx, db, log, publish)
			}
		}
	}()
}

func runOnce(ctx context.Context, db *bun.DB, log *slog.Logger, publish func(slug, typ string, entry map[string]any, id string)) {
	var entries []dbmodels.CollectionEntry
	err := db.NewSelect().
		Model(&entries).
		Where("status = ?", "draft").
		Where("publish_at IS NOT NULL").
		Where("publish_at <= ?", time.Now().UTC()).
		Scan(ctx)
	if err != nil {
		log.Error("scheduler: query pending entries", "error", err)
		return
	}

	now := time.Now().UTC()
	for _, e := range entries {
		_, err := db.NewUpdate().
			Model((*dbmodels.CollectionEntry)(nil)).
			Set("status = ?", "published").
			Set("updated_at = ?", now).
			Where("id = ?", e.ID).
			Exec(ctx)
		if err != nil {
			log.Error("scheduler: publish entry", "id", e.ID, "error", err)
			continue
		}

		log.Info("scheduler: published entry", "id", e.ID, "collection", e.CollectionSlug)

		var dataMap map[string]any
		if err := json.Unmarshal(e.Data, &dataMap); err != nil {
			dataMap = map[string]any{}
		}
		publish(e.CollectionSlug, "entry.updated", map[string]any{
			"id":         e.ID,
			"status":     "published",
			"created_at": e.CreatedAt,
			"updated_at": now,
			"data":       dataMap,
		}, e.ID)
	}
}
