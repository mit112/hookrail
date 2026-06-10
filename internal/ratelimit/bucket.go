// Package ratelimit is the in-process token bucket of §4 (Redis-backed
// distributed version is P2). Used per-endpoint by workers and per-key at
// ingress (§10).
package ratelimit

import (
	"sync"
	"time"
)

type Bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
	rate   float64 // tokens per second
	burst  float64
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

// Registry lazily creates one bucket per key (endpoint id / producer key id).
type Registry struct {
	mu      sync.Mutex
	buckets map[string]*Bucket
	rate    float64
	burst   float64
}

func NewRegistry(defaultRate, defaultBurst float64) *Registry {
	return &Registry{buckets: map[string]*Bucket{}, rate: defaultRate, burst: defaultBurst}
}

func (r *Registry) Allow(key string, now time.Time) bool {
	r.mu.Lock()
	b, ok := r.buckets[key]
	if !ok {
		b = NewBucket(r.rate, r.burst)
		r.buckets[key] = b
	}
	r.mu.Unlock()
	return b.Allow(now)
}
