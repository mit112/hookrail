// internal/scheduler/retention.go
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/obs"
	"github.com/mit112/hookrail/internal/store"
)

type Janitor struct {
	Store      *store.Store
	PayloadAge time.Duration // RETENTION_EVENT_PAYLOAD_DAYS
	AttemptAge time.Duration // RETENTION_ATTEMPT_DAYS
	Batch      int           // RETENTION_BATCH
	Interval   time.Duration // RETENTION_INTERVAL
	TickBudget time.Duration // RETENTION_TICK_BUDGET
}

// RunOnce executes all four passes once, each capped by the tick budget. It
// AGGREGATES failures (and budget expiry) into a returned error so the CLI can
// exit nonzero and the scheduler can log honestly — a job failure must never
// surface as success (design honesty).
func (j *Janitor) RunOnce(ctx context.Context) error {
	start := time.Now()
	defer func() { obs.RetentionTickSeconds.Observe(time.Since(start).Seconds()) }()
	bctx, cancel := context.WithTimeout(ctx, j.TickBudget)
	defer cancel()

	var errs []error
	run := func(job string, fn func() (int, error)) {
		n, err := fn()
		if err != nil {
			slog.Warn("retention job failed", "job", job, "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", job, err))
			return
		}
		if n > 0 {
			obs.RetentionRowsPruned.WithLabelValues(job).Add(float64(n))
			slog.Info("retention pruned", "job", job, "rows", n)
		}
	}
	run("tombstone", func() (int, error) { return j.Store.TombstoneEventPayloads(bctx, j.PayloadAge, j.Batch) })
	run("attempt_trim", func() (int, error) { return j.Store.TrimDeliveryAttempts(bctx, j.AttemptAge, j.Batch) })
	run("idempotency", func() (int, error) { return j.Store.PurgeIdempotency(bctx, j.Batch) })
	run("cancel_orphaned", func() (int, error) { return j.Store.CancelOrphanedLocked(bctx, j.Batch) })
	if err := bctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("tick budget: %w", err))
	}
	return errors.Join(errs...)
}

// Run ticks on the interval, running immediately on startup. A failing tick is
// logged (the scheduler keeps running); the CLI path treats it as a hard error.
func (j *Janitor) Run(ctx context.Context) {
	if err := j.RunOnce(ctx); err != nil {
		slog.Error("retention tick had failures", "err", err)
	}
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.RunOnce(ctx); err != nil {
				slog.Error("retention tick had failures", "err", err)
			}
		}
	}
}
