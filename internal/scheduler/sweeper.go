// Package scheduler is the PG sweeper (§3.3): the durability repair loop.
// Redis losing a message only ever delays a delivery by one sweep interval.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

type Source interface {
	DueDeliveryIDs(ctx context.Context, afterID string, limit int) ([]string, error)
}

type Publisher interface {
	Publish(ctx context.Context, deliveryID string) error
}

type Sweeper struct {
	Source    Source
	Publisher Publisher
	Interval  time.Duration // 30s in production (§3.3)
	BatchSize int
}

// RunOnce walks the WHOLE due set in keyset batches and publishes every id —
// a single capped batch would republish the same first rows each sweep while
// later rows starve, breaking the <60s recovery story (§11). Publish failures
// are logged and skipped: the row stays due, the next sweep retries it, and
// the cursor advances past it so one bad id can't block the rest.
func (sw *Sweeper) RunOnce(ctx context.Context) (int, error) {
	total := 0
	cursor := ""
	for {
		ids, err := sw.Source.DueDeliveryIDs(ctx, cursor, sw.BatchSize)
		if err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		for _, id := range ids {
			if err := sw.Publisher.Publish(ctx, id); err != nil {
				slog.Warn("sweeper publish failed", "delivery_id", id, "err", err)
				continue
			}
			total++
		}
		cursor = ids[len(ids)-1]
		if len(ids) < sw.BatchSize {
			return total, nil
		}
	}
}

// Run sweeps immediately on startup (§3.3: "on startup + every 30s"), then on the interval.
func (sw *Sweeper) Run(ctx context.Context) error {
	if _, err := sw.RunOnce(ctx); err != nil {
		slog.Error("startup sweep failed", "err", err)
	}
	t := time.NewTicker(sw.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if n, err := sw.RunOnce(ctx); err != nil {
				slog.Error("sweep failed", "err", err)
			} else if n > 0 {
				slog.Info("sweep republished", "count", n)
			}
		}
	}
}
