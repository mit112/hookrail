// Package scheduler is the PG sweeper (§3.3): the durability repair loop.
// Redis losing a message only ever delays a delivery by one sweep interval.
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/obs"
)

type Source interface {
	DueDeliveryIDs(ctx context.Context, afterID string, limit int) ([]string, error)
}

type Publisher interface {
	Publish(ctx context.Context, deliveryID string) error
}

// Reconciler self-heals the ordered-keys cursor projection and republishes lost
// heads. Optional on the Sweeper — nil disables the ordered reconcile pass.
type Reconciler interface {
	ReconcileOrderedKeys(ctx context.Context, batch int, publish func(context.Context, string) error) (blockedKeys int, totalBacklog int64, err error)
}

type Sweeper struct {
	Source     Source
	Publisher  Publisher
	Reconciler Reconciler    // optional: ordered-keys reconcile + gauge emit
	Interval   time.Duration // 30s in production (§3.3)
	BatchSize  int
}

// reconcileOrdered runs one ordered-keys reconcile pass and updates the
// blocked/backlog gauges (a real metric emit path, §7). Errors are logged and
// swallowed: the next tick retries, and the due sweep is the delivery backstop.
func (sw *Sweeper) reconcileOrdered(ctx context.Context) {
	if sw.Reconciler == nil {
		return
	}
	blocked, backlog, err := sw.Reconciler.ReconcileOrderedKeys(ctx, sw.BatchSize, sw.Publisher.Publish)
	if err != nil {
		slog.Warn("ordered reconcile failed", "err", err)
		return
	}
	obs.OrderedKeysBlocked.Set(float64(blocked))
	obs.OrderedKeyBacklog.Set(float64(backlog))
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
			obs.SweeperRepublished.Inc()
			total++
		}
		cursor = ids[len(ids)-1]
		if len(ids) < sw.BatchSize {
			return total, nil
		}
	}
}

// Startup performs the initial sweep + reconcile on election.
// It is the Elector's onElected callback.
func (sw *Sweeper) Startup(ctx context.Context) error {
	if _, err := sw.RunOnce(ctx); err != nil {
		slog.Error("startup sweep failed", "err", err)
		return err
	}
	sw.reconcileOrdered(ctx)
	return nil
}

// Cycle performs one sweep + reconcile tick. It is the Elector's do callback;
// only the leader calls it. Returns the error from RunOnce (or nil).
func (sw *Sweeper) Cycle(ctx context.Context) error {
	n, err := sw.RunOnce(ctx)
	if err != nil {
		slog.Error("sweep failed", "err", err)
	} else if n > 0 {
		slog.Info("sweep republished", "count", n)
	}
	sw.reconcileOrdered(ctx)
	return err
}

// Run sweeps immediately on startup (§3.3: "on startup + every 30s"), then on
// the interval. Retained for non-HA/dev use; production goes through the Elector
// which gates Startup/Cycle on leadership.
func (sw *Sweeper) Run(ctx context.Context) error {
	_ = sw.Startup(ctx)
	t := time.NewTicker(sw.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_ = sw.Cycle(ctx)
		}
	}
}
