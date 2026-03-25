package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"

	dbmodels "github.com/bkincz/reverb/db/models"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type JobOptions struct {
	MaxAttempts int
	Backoff     time.Duration
}

type registeredHandler struct {
	fn   func(ctx context.Context, payload []byte) error
	opts JobOptions
}

type Queue struct {
	db          *bun.DB
	log         *slog.Logger
	concurrency int
	handlers    map[string]registeredHandler
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

func New(db *bun.DB, log *slog.Logger, concurrency int) *Queue {
	if concurrency <= 0 {
		concurrency = 5
	}
	return &Queue{
		db:          db,
		log:         log,
		concurrency: concurrency,
		handlers:    make(map[string]registeredHandler),
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func (q *Queue) Register(name string, fn func(ctx context.Context, payload []byte) error, opts JobOptions) {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.Backoff <= 0 {
		opts.Backoff = 5 * time.Second
	}
	q.handlers[name] = registeredHandler{fn: fn, opts: opts}
}

// ---------------------------------------------------------------------------
// Enqueue
// ---------------------------------------------------------------------------

func (q *Queue) Enqueue(ctx context.Context, name string, payload any) error {
	return q.EnqueueAt(ctx, name, payload, time.Now().UTC())
}

func (q *Queue) EnqueueAt(ctx context.Context, name string, payload any, runAt time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("jobs: marshal payload: %w", err)
	}

	h, ok := q.handlers[name]
	maxAttempts := 3
	if ok {
		maxAttempts = h.opts.MaxAttempts
	}

	job := &dbmodels.Job{
		ID:          uuid.New().String(),
		Name:        name,
		Payload:     raw,
		Status:      "pending",
		MaxAttempts: maxAttempts,
		RunAt:       runAt.UTC(),
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	if _, err := q.db.NewInsert().Model(job).Exec(ctx); err != nil {
		return fmt.Errorf("jobs: enqueue %q: %w", name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Worker
// ---------------------------------------------------------------------------

func (q *Queue) Start(ctx context.Context) {
	for i := 0; i < q.concurrency; i++ {
		go q.worker(ctx)
	}
	go q.recoverStuck(ctx)
}

func (q *Queue) worker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.processOne(ctx)
		}
	}
}

func (q *Queue) processOne(ctx context.Context) {
	job, err := q.claimOne(ctx)
	if err != nil {
		q.log.Error("jobs: claim", "err", err)
		return
	}
	if job == nil {
		return
	}

	h, ok := q.handlers[job.Name]
	if !ok {
		q.fail(ctx, job, fmt.Errorf("jobs: no handler registered for %q", job.Name))
		return
	}

	if err := h.fn(ctx, job.Payload); err != nil {
		q.fail(ctx, job, err)
		return
	}

	q.done(ctx, job)
}

func (q *Queue) claimOne(ctx context.Context) (*dbmodels.Job, error) {
	isSQLite := q.db.Dialect().Name() == dialect.SQLite

	var job dbmodels.Job
	err := q.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		sel := tx.NewSelect().
			Model(&job).
			Where("status = ?", "pending").
			Where("run_at <= ?", time.Now().UTC()).
			OrderExpr("run_at ASC").
			Limit(1)

		if !isSQLite {
			sel = sel.For("UPDATE SKIP LOCKED")
		}

		if err := sel.Scan(ctx); err != nil {
			return err
		}

		job.Status = "running"
		job.Attempts++
		job.UpdatedAt = time.Now().UTC()
		_, err := tx.NewUpdate().Model(&job).WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("jobs: claim: %w", err)
	}
	return &job, nil
}

func (q *Queue) done(ctx context.Context, job *dbmodels.Job) {
	job.Status = "done"
	job.UpdatedAt = time.Now().UTC()
	if _, err := q.db.NewUpdate().Model(job).WherePK().Exec(ctx); err != nil {
		q.log.Error("jobs: mark done", "id", job.ID, "err", err)
	}
}

func (q *Queue) fail(ctx context.Context, job *dbmodels.Job, jobErr error) {
	h, ok := q.handlers[job.Name]
	backoff := 5 * time.Second
	if ok {
		backoff = h.opts.Backoff
	}

	if job.Attempts >= job.MaxAttempts {
		job.Status = "failed"
		job.LastError = jobErr.Error()
	} else {
		delay := backoff * (1 << (job.Attempts - 1))
		if delay > time.Hour {
			delay = time.Hour
		}
		job.Status = "pending"
		job.RunAt = time.Now().UTC().Add(delay)
		job.LastError = jobErr.Error()
	}
	job.UpdatedAt = time.Now().UTC()

	if _, err := q.db.NewUpdate().Model(job).WherePK().Exec(ctx); err != nil {
		q.log.Error("jobs: mark failed", "id", job.ID, "err", err)
	}
}

func (q *Queue) recoverStuck(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-5 * time.Minute)
			res, err := q.db.NewUpdate().
				Model((*dbmodels.Job)(nil)).
				Set("status = ?", "pending").
				Set("updated_at = ?", time.Now().UTC()).
				Where("status = ?", "running").
				Where("updated_at < ?", cutoff).
				Exec(ctx)
			if err != nil {
				q.log.Error("jobs: recover stuck", "err", err)
				continue
			}
			n, _ := res.RowsAffected()
			if n > 0 {
				q.log.Info("jobs: recovered stuck jobs", "count", n)
			}
		}
	}
}
