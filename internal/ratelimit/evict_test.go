package ratelimit

import (
	"testing"
	"time"
)

// Idle buckets are evicted so the map cannot grow unbounded, but a used bucket
// within idleTTL is retained, and a bucket configured via SetRate but not yet
// used is NOT dropped (its pending reconfiguration must survive).
func TestRegistryEvictsIdleBuckets(t *testing.T) {
	r := NewRegistry(100, 100)
	base := time.Unix(1_700_000_000, 0)

	r.Allow("hot", base)
	r.Allow("cold", base)
	r.SetRate("configured-unused", 1, 1) // created, never Allow'd → last is zero
	// A configured endpoint that HAS been used and then goes idle must keep its
	// override — recreating it at registry defaults would drop the endpoint's cap.
	r.SetRate("configured-idle", 1, 1)
	r.Allow("configured-idle", base)

	// A sweep well past idleTTL, but "hot" is used again at sweep time.
	future := base.Add(idleTTL + time.Minute)
	r.Allow("hot", future)

	r.mu.Lock()
	_, hot := r.buckets["hot"]
	_, cold := r.buckets["cold"]
	_, cfgUnused := r.buckets["configured-unused"]
	_, cfgIdle := r.buckets["configured-idle"]
	r.mu.Unlock()

	if !hot {
		t.Error("recently-used bucket must be retained")
	}
	if cold {
		t.Error("idle default bucket must be evicted")
	}
	if !cfgUnused {
		t.Error("configured-but-unused bucket must not be evicted")
	}
	if !cfgIdle {
		t.Error("configured bucket idle past ttl must NOT be evicted (would drop its override)")
	}
}
