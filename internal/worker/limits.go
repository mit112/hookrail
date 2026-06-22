package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/mit112/hookrail/internal/obs"
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
	Global       *ratelimit.GlobalLimiter // when non-nil, Refresh publishes override snapshot here
	Fallback     *ratelimit.Registry      // per-endpoint fallback rates (shadow-debited on global allow)
	Interval     time.Duration
	DefaultRate  float64
	DefaultBurst float64
	applied      map[string]struct{} // endpoints currently carrying an override
	lastSuccess  time.Time           // last successful global snapshot publish, for config-age metric
}

func (e *EndpointLimits) Run(ctx context.Context) {
	t := time.NewTicker(e.Interval)
	defer t.Stop()
	_ = e.Refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = e.Refresh(ctx)
		}
		if e.Global != nil {
			obs.RatelimitConfigAge.Set(time.Since(e.lastSuccess).Seconds())
		}
	}
}

// Refresh pulls current per-endpoint limits and reconciles the registry. Exported
// so tests can drive one cycle deterministically. Returns an error only when the
// store call fails (callers in main may fail boot on first load).
func (e *EndpointLimits) Refresh(ctx context.Context) error {
	limits, err := e.Store.EndpointRateLimits(ctx)
	if err != nil {
		slog.Warn("endpoint limits refresh failed; keeping previous", "err", err)
		return err
	}

	// Build the global snapshot + update the fallback registry.
	// Drop the old applied-revert dance for the global path: membership lies
	// entirely in the swapped snapshot. An endpoint not in the map routes to
	// the local default Registry in the worker (allowDelivery router).
	if e.Global != nil {
		m := make(map[string]ratelimit.Limit, len(limits))
		for ep, rps := range limits {
			burst := rps * 2
			if burst < 1 {
				burst = 1
			}
			m[ep] = ratelimit.Limit{Rate: rps, Burst: burst}
			e.Fallback.SetRate(ep, rps, burst)
		}
		// Reconcile removals in the fallback: endpoints that dropped out revert
		// to the worker default in the fallback Registry (the global snapshot
		// already lacks them after the swap).
		if e.applied == nil {
			e.applied = map[string]struct{}{}
		}
		for ep := range e.applied {
			if _, still := m[ep]; !still {
				e.Fallback.SetRate(ep, e.DefaultRate, e.DefaultBurst)
			}
		}
		// Build next applied set from the new map keys
		next := make(map[string]struct{}, len(m))
		for ep := range m {
			next[ep] = struct{}{}
		}
		e.applied = next
		e.Global.SetSnapshot(m)
		e.lastSuccess = time.Now()
		obs.RatelimitConfigAge.Set(0)
		return nil
	}

	// Pure-local path (no GlobalLimiter configured): keep the existing behavior.
	if e.applied == nil {
		e.applied = map[string]struct{}{}
	}
	next := make(map[string]struct{}, len(limits))
	for ep, rps := range limits {
		burst := rps * 2
		if burst < 1 {
			burst = 1
		}
		e.Registry.SetRate(ep, rps, burst)
		next[ep] = struct{}{}
	}
	for ep := range e.applied {
		if _, still := next[ep]; !still {
			e.Registry.SetRate(ep, e.DefaultRate, e.DefaultBurst)
		}
	}
	e.applied = next
	return nil
}
