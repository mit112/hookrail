package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Limiter is the RedisLimiter's minimal interface, exported so tests can
// inject fakes.
type Limiter interface {
	Allow(ctx context.Context, key string, rate, burst float64, now time.Time) (bool, error)
}

// Limit is a rate/burst pair for one endpoint override.
type Limit struct {
	Rate  float64
	Burst float64
}

// GlobalLimiter wraps a Redis-backed RedisLimiter with an atomic override
// snapshot, a shadow-debited local fallback Registry, and a per-process
// circuit breaker. On limiter-command failure (queue Redis stays healthy)
// it fails open to the fallback.
type GlobalLimiter struct {
	rl       Limiter
	fallback *Registry
	timeout  time.Duration
	snap     atomic.Pointer[map[string]Limit]
	br       *breaker
}

// NewGlobalLimiterWith is the test constructor that accepts an arbitrary
// Limiter.
func NewGlobalLimiterWith(rl Limiter, fb *Registry, timeout time.Duration) *GlobalLimiter {
	return &GlobalLimiter{rl: rl, fallback: fb, timeout: timeout, br: newBreaker(5, 2*time.Second)}
}

// NewGlobalLimiter creates a GlobalLimiter backed by a RedisLimiter.
func NewGlobalLimiter(rl *RedisLimiter, fallback *Registry, timeout time.Duration) *GlobalLimiter {
	return NewGlobalLimiterWith(rl, fallback, timeout)
}

// SetSnapshot publishes a fresh override map. The map is cloned so the
// published snapshot is immutable regardless of what the caller does with its
// copy afterwards, and atomically swapped (copy-on-write — readers never see a
// torn map).
func (g *GlobalLimiter) SetSnapshot(m map[string]Limit) {
	cp := make(map[string]Limit, len(m))
	for k, v := range m {
		cp[k] = v
	}
	g.snap.Store(&cp)
}

// Has reports whether the endpoint has a global override in the current
// snapshot.
func (g *GlobalLimiter) Has(ep string) bool {
	m := g.snap.Load()
	if m == nil {
		return false
	}
	_, ok := (*m)[ep]
	return ok
}

// Decide makes the rate-limit decision for one delivery with a SINGLE atomic
// snapshot load (no torn Has()+Allow() race): a concurrent SetSnapshot can
// never flip routing between the membership test and the decision.
//
//	handled=false → the endpoint has no global override; the caller must use
//	                its local per-worker Registry (non-override path, unchanged).
//	handled=true  → mode is "global" (Redis-backed decision) or "failopen"
//	                (limiter-command failure / breaker open).
//
// FAIL-OPEN SCOPE (deliberate): on a limiter-COMMAND failure or an open breaker
// the queue Redis is assumed healthy (whole-Redis-down is the separate queue-loss
// path). We degrade to the per-worker local fallback bucket — NOT unconditional
// admit. A rate limiter exists to protect the receiver, so the safe degradation
// is "fall back to P1 per-worker limiting"; a fallback DENY releases the claim
// and reschedules (no attempt consumed, never lost), so liveness holds while the
// local bucket refills. Unconditionally admitting the whole backlog at a receiver
// during a limiter blip would be the unsafe choice.
func (g *GlobalLimiter) Decide(ctx context.Context, ep string, now time.Time) (handled, allowed bool, mode string) {
	m := g.snap.Load()
	var lim Limit
	var ok bool
	if m != nil {
		lim, ok = (*m)[ep]
	}
	if !ok {
		return false, false, "" // not an override endpoint: caller uses local Registry
	}
	if g.br.open(now) {
		return true, g.fallback.Allow(ep, now), "failopen"
	}
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	a, err := g.rl.Allow(cctx, ep, lim.Rate, lim.Burst, now)
	if err != nil {
		g.br.fail(now)
		return true, g.fallback.Allow(ep, now), "failopen"
	}
	g.br.ok()
	if a {
		g.fallback.Allow(ep, now) // shadow-debit: keep the fallback warm for a future fail-open
	}
	return true, a, "global"
}

// Allow is the legacy two-return shim retained for unit tests. Production code
// routes through Decide. For an endpoint absent from the snapshot it preserves
// the old defensive fail-open-to-fallback behavior.
func (g *GlobalLimiter) Allow(ctx context.Context, ep string, now time.Time) (bool, string) {
	handled, allowed, mode := g.Decide(ctx, ep, now)
	if !handled {
		return g.fallback.Allow(ep, now), "failopen"
	}
	return allowed, mode
}

// breaker is a simple consecutive-failure circuit breaker. After threshold
// consecutive failures, it opens for a cooldown.
type breaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openUntil time.Time
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: threshold, cooldown: cooldown}
}

func (b *breaker) open(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return false
	}
	if now.Before(b.openUntil) {
		return true
	}
	// cooldown expired: half-open → closed on next ok()
	b.openUntil = time.Time{}
	b.failures = 0
	return false
}

func (b *breaker) fail(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = now.Add(b.cooldown)
	}
}

func (b *breaker) ok() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openUntil = time.Time{}
}

// FallbackForTest exposes the fallback Registry for test assertions.
func (g *GlobalLimiter) FallbackForTest() *Registry {
	return g.fallback
}
