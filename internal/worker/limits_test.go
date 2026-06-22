//go:build integration

package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/store"
	"github.com/mit112/hookrail/internal/worker"
)

var (
	rlRedisOnce sync.Once
	rlRedis     *redis.Client
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rlRedisOnce.Do(func() {
		ctx := context.Background()
		rc, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			t.Fatalf("redis container: %v", err)
		}
		ep, err := rc.ConnectionString(ctx)
		if err != nil {
			t.Fatal(err)
		}
		opt, err := redis.ParseURL(ep)
		if err != nil {
			t.Fatalf("parse redis URL: %v", err)
		}
		rlRedis = redis.NewClient(opt)
	})
	return rlRedis
}

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
	_ = el.Refresh(ctx) // applies the 1 rps / burst 2 override for epID

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
	_ = el.Refresh(ctx)
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

// A valid low rps (chk_rate only requires > 0) whose 2x burst is < 1 must still
// deliver: without the burst floor, the bucket never accrues a full token and
// delivery to that endpoint stalls forever (Codex M-A4a pre-gate BLOCKER).
func TestEndpointLimitsLowRateStillDelivers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	slow := 0.1 // burst would be 0.2 (< 1) without the floor
	_, _ = s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "s.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &slow})

	reg := ratelimit.NewRegistry(1000, 2000)
	el := &worker.EndpointLimits{Store: s, Registry: reg, Interval: time.Hour, DefaultRate: 1000, DefaultBurst: 2000}
	_ = el.Refresh(ctx)

	if !reg.Allow(epID, time.Now()) {
		t.Fatal("a 0.1 rps endpoint must deliver at least once (burst floored to 1); delivery would stall forever otherwise")
	}
}

func TestRefreshBuildsGlobalSnapshot(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	low := 5.0
	_, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "z.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &low})
	if err != nil {
		t.Fatal(err)
	}
	rdb := testRedis(t)
	rl := ratelimit.NewRedisLimiter(rdb, time.Minute)
	g := ratelimit.NewGlobalLimiter(rl, ratelimit.NewRegistry(1000, 2000), 50*time.Millisecond)
	el := &worker.EndpointLimits{
		Store: s, Registry: ratelimit.NewRegistry(1000, 2000),
		Global: g, Fallback: g.FallbackForTest(),
		Interval: time.Hour, DefaultRate: 1000, DefaultBurst: 2000,
	}
	_ = el.Refresh(ctx)
	if !g.Has(epID) {
		t.Fatal("override endpoint must be in the global snapshot")
	}
}

func TestRefreshRemovesDroppedOverride(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	epID, _, _ := s.CreateEndpoint(ctx, [32]byte{}, "https://example.com/h", "")
	low := 5.0
	subID, err := s.CreateSubscriptionFull(ctx, store.SubInput{TopicPattern: "z.*", EndpointID: epID, MaxAttempts: 3, RateLimitRPS: &low})
	if err != nil {
		t.Fatal(err)
	}
	rdb := testRedis(t)
	rl := ratelimit.NewRedisLimiter(rdb, time.Minute)
	fb := ratelimit.NewRegistry(1000, 2000)
	g := ratelimit.NewGlobalLimiter(rl, fb, 50*time.Millisecond)
	el := &worker.EndpointLimits{
		Store: s, Registry: ratelimit.NewRegistry(1000, 2000),
		Global: g, Fallback: fb,
		Interval: time.Hour, DefaultRate: 1000, DefaultBurst: 2000,
	}
	_ = el.Refresh(ctx)
	if !g.Has(epID) {
		t.Fatal("override endpoint must be in the global snapshot before removal")
	}
	// Soft-delete the limiting sub → drops out of EndpointRateLimits
	if err := s.SoftDeleteSubscription(ctx, subID); err != nil {
		t.Fatal(err)
	}
	_ = el.Refresh(ctx)
	if g.Has(epID) {
		t.Fatal("removed override must NOT be in the global snapshot (routes back to local default)")
	}
}
