//go:build integration

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

// Reuses the worker package's existing integration testStore (worker_test.go).
// time is driven explicitly: the token bucket only accrues on elapsed wall
// time (bucket.go Allow), and SetRate does NOT refill — so the post-reset
// checks MUST advance the clock or they would all see 0 tokens.
func TestEndpointLimitsRevertsRemoved(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	low := 1.0
	subID, _ := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "z.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &low})

	reg := ratelimit.NewRegistry(1000, 2000)
	el := &worker.EndpointLimits{Store: s, Registry: reg, Interval: time.Hour, DefaultRate: 1000, DefaultBurst: 2000}
	el.Refresh(ctx) // applies the 1 rps / burst 2 override for epID

	// throttled to ~1 rps: burst 2 allowed at t0, third denied (no time elapsed)
	t0 := time.Now()
	if ok1, ok2 := reg.Allow(epID, t0), reg.Allow(epID, t0); !ok1 || !ok2 {
		t.Fatal("burst of 2 should be allowed")
	}
	if reg.Allow(epID, t0) {
		t.Fatal("endpoint should be throttled to its 1 rps override after burst")
	}
	// remove the only limiting sub, refresh → bucket reverts to the 1000 default.
	_ = s.SoftDeleteSubscription(ctx, subID)
	el.Refresh(ctx)
	// advance 1s so the reverted 1000 rps bucket accrues ~1000 tokens (capped at
	// the 2000 burst); the old 1 rps bucket would only accrue 1 in the same second.
	t1 := t0.Add(time.Second)
	allowed := 0
	for i := 0; i < 50; i++ {
		if reg.Allow(epID, t1) {
			allowed++
		}
	}
	if allowed < 50 {
		t.Fatalf("after removal the endpoint stayed throttled (allowed=%d/50) — stale override not reverted", allowed)
	}
}
