// Package ratelimit is the in-process token bucket of §4. The Redis-backed
// distributed version is implemented for the worker delivery path (P2); see
// redisbucket.go / global.go. Used per-endpoint by workers and per-key at
// ingress (§10).
package ratelimit

import (
	"sync"
	"time"
)

type Bucket struct {
	mu         sync.Mutex
	tokens     float64
	last       time.Time
	rate       float64 // tokens per second
	burst      float64
	configured bool // true once SetRate applied a per-key override; exempt from eviction
}

func NewBucket(rate, burst float64) *Bucket {
	return &Bucket{tokens: burst, rate: rate, burst: burst}
}

func (b *Bucket) Allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.last.IsZero() {
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// idleTTL is how long a bucket may sit untouched before it is evicted. A bucket
// idle this long has already refilled to burst, so dropping it is behaviorally
// a no-op — the next Allow recreates it full. Bounds memory in a long-lived
// process against churn of endpoint ids / producer keys (rotations, deletes).
const idleTTL = 30 * time.Minute

// Registry lazily creates one bucket per key (endpoint id / producer key id)
// and evicts buckets that have gone idle so the map does not grow unbounded.
type Registry struct {
	mu        sync.Mutex
	buckets   map[string]*Bucket
	rate      float64
	burst     float64
	lastSweep time.Time
}

func NewRegistry(defaultRate, defaultBurst float64) *Registry {
	return &Registry{buckets: map[string]*Bucket{}, rate: defaultRate, burst: defaultBurst}
}

func (r *Registry) Allow(key string, now time.Time) bool {
	r.mu.Lock()
	r.sweepLocked(now)
	b, ok := r.buckets[key]
	if !ok {
		b = NewBucket(r.rate, r.burst)
		r.buckets[key] = b
	}
	r.mu.Unlock()
	return b.Allow(now)
}

// sweepLocked drops idle buckets. Called with r.mu held; it acquires each
// bucket's mu only after r.mu (a fixed r→bucket lock order that no other path
// inverts), so it cannot deadlock. Runs at most once per idleTTL.
func (r *Registry) sweepLocked(now time.Time) {
	if !r.lastSweep.IsZero() && now.Sub(r.lastSweep) < idleTTL {
		return
	}
	r.lastSweep = now
	for k, b := range r.buckets {
		b.mu.Lock()
		// Never evict a bucket carrying a per-key SetRate override: recreating it
		// from the registry defaults would silently drop an endpoint's configured
		// rate_limit_rps until the next limits refresh. Only default-rate buckets
		// that were used and then went idle past idleTTL are dropped (a fresh
		// default bucket refills to burst, so this is behaviorally a no-op).
		idle := !b.configured && !b.last.IsZero() && now.Sub(b.last) > idleTTL
		b.mu.Unlock()
		if idle {
			delete(r.buckets, k)
		}
	}
}

// SetRate reconfigures an existing bucket's rate and burst (design §4.3:
// per-key reconfiguration; buckets were immutable in P0). Tokens are clamped
// to the new burst so a shrink takes effect immediately.
func (b *Bucket) SetRate(rate, burst float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rate = rate
	b.burst = burst
	b.configured = true
	if b.tokens > burst {
		b.tokens = burst
	}
}

// SetRate gets-or-creates the key's bucket and reconfigures it.
func (r *Registry) SetRate(key string, rate, burst float64) {
	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = NewBucket(rate, burst)
		b.configured = true
		r.buckets[key] = b
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()
	b.SetRate(rate, burst)
}
