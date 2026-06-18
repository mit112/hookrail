package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
)

// EndpointLimits periodically pushes per-endpoint MIN(rate_limit_rps) into the
// worker's in-process Registry (design §4.3). Best-effort and per-worker: this
// is NOT a deployment-wide cap (true global cap is P2). A PATCH propagates
// within one Interval.
type EndpointLimits struct {
	Store        *store.Store
	Registry     *ratelimit.Registry
	Interval     time.Duration // e.g. 15s
	DefaultRate  float64       // worker default rps — what a reverted endpoint goes back to
	DefaultBurst float64
	applied      map[string]struct{} // endpoints currently carrying an override
}

func (e *EndpointLimits) Run(ctx context.Context) {
	t := time.NewTicker(e.Interval)
	defer t.Stop()
	e.Refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Refresh(ctx)
		}
	}
}

// Refresh pulls current per-endpoint limits and reconciles the registry. Exported
// so tests can drive one cycle deterministically.
func (e *EndpointLimits) Refresh(ctx context.Context) {
	limits, err := e.Store.EndpointRateLimits(ctx)
	if err != nil {
		slog.Warn("endpoint limits refresh failed; keeping previous", "err", err)
		return
	}
	if e.applied == nil {
		e.applied = map[string]struct{}{}
	}
	next := make(map[string]struct{}, len(limits))
	for ep, rps := range limits {
		e.Registry.SetRate(ep, rps, rps*2)
		next[ep] = struct{}{}
	}
	// Reconcile removals: an endpoint whose last limiting sub was paused,
	// deleted, or had rps cleared drops out of the query — revert its bucket to
	// the worker default, else it stays throttled at the old rate forever.
	for ep := range e.applied {
		if _, still := next[ep]; !still {
			e.Registry.SetRate(ep, e.DefaultRate, e.DefaultBurst)
		}
	}
	e.applied = next
}
