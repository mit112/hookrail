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

// SetSnapshot publishes a fresh override map. Immutable, atomically swapped.
func (g *GlobalLimiter) SetSnapshot(m map[string]Limit) {
	g.snap.Store(&m)
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

// Allow checks the global limiter for the endpoint; returns (allowed, mode)
// where mode is "global" or "failopen".
func (g *GlobalLimiter) Allow(ctx context.Context, ep string, now time.Time) (bool, string) {
	m := g.snap.Load()
	var lim Limit
	var ok bool
	if m != nil {
		lim, ok = (*m)[ep]
	}
	if !ok {
		return g.fallback.Allow(ep, now), "failopen" // defensive: caller should gate on Has
	}
	if g.br.open(now) {
		return g.fallback.Allow(ep, now), "failopen"
	}
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	allowed, err := g.rl.Allow(cctx, ep, lim.Rate, lim.Burst, now)
	if err != nil {
		g.br.fail(now)
		return g.fallback.Allow(ep, now), "failopen"
	}
	g.br.ok()
	if allowed {
		g.fallback.Allow(ep, now) // shadow-debit: keep the fallback warm
	}
	return allowed, "global"
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
