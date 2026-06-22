package ratelimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/redis/go-redis/v9"
)

var (
	rdbOnce sync.Once
	rdb     *redis.Client
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdbOnce.Do(func() {
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
		rdb = redis.NewClient(opt)
	})
	return rdb
}

func TestRedisLimiter_BurstThenDenyThenRefill(t *testing.T) {
	rdb := testRedis(t)
	l := NewRedisLimiter(rdb, time.Minute)
	ctx := context.Background()
	base := time.Unix(1700000000, 0)
	rate, burst := 1.0, 3.0 // 1 rps, burst 3
	// burst=3 -> first 3 allowed at the same instant, 4th denied
	for i := 0; i < 3; i++ {
		ok, err := l.Allow(ctx, "ep1", rate, burst, base)
		if err != nil || !ok {
			t.Fatalf("token %d: ok=%v err=%v", i, ok, err)
		}
	}
	if ok, _ := l.Allow(ctx, "ep1", rate, burst, base); ok {
		t.Fatal("4th should be denied")
	}
	// 2s later -> 2 tokens refilled
	if ok, _ := l.Allow(ctx, "ep1", rate, burst, base.Add(2*time.Second)); !ok {
		t.Fatal("after 2s should allow")
	}
}

func TestRedisLimiter_ConcurrentExactCount(t *testing.T) {
	rdb := testRedis(t)
	l := NewRedisLimiter(rdb, time.Minute)
	now := time.Unix(1700000000, 0) // frozen clock -> no refill -> exactly burst allowed
	rate, burst := 100.0, 50.0
	var allowed int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow(context.Background(), "epC", rate, burst, now); ok {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != int64(burst) {
		t.Fatalf("want exactly %v allowed, got %d", burst, allowed)
	}
}

func TestRedisLimiter_BackwardClockClamped(t *testing.T) {
	rdb := testRedis(t)
	l := NewRedisLimiter(rdb, time.Minute)
	rate, burst := 1.0, 1.0
	t1 := time.Unix(1700000000, 0)
	_, err := l.Allow(context.Background(), "epB", rate, burst, t1) // consume the 1 token
	if err != nil {
		t.Fatalf("initial allow: %v", err)
	}
	// a lagging replica 5s in the past must NOT refill (elapsed clamped to >=0)
	if ok, _ := l.Allow(context.Background(), "epB", rate, burst, t1.Add(-5*time.Second)); ok {
		t.Fatal("backward clock must not add tokens")
	}
}

func TestRedisLimiter_TTLSubOneRPS(t *testing.T) {
	if got := TTLSecondsForTest(0.1, 1, time.Minute); got < 60 {
		t.Fatalf("ttl floor: %d", got)
	} // 60s floor wins
	if got := TTLSecondsForTest(0.01, 5, time.Minute); got < 500 {
		t.Fatalf("ceil(5/0.01)=500 must win: %d", got)
	}
}

func TestRedisLimiter_FlushReconstructsFull(t *testing.T) {
	rdb := testRedis(t)
	l := NewRedisLimiter(rdb, time.Minute)
	now := time.Unix(1700000000, 0)
	rate, burst := 1.0, 3.0
	for i := 0; i < 3; i++ {
		if ok, err := l.Allow(context.Background(), "epF", rate, burst, now); err != nil || !ok {
			t.Fatalf("allow %d: ok=%v err=%v", i, ok, err)
		}
	}
	rdb.FlushAll(context.Background()) // simulate Redis state loss
	ok, _ := l.Allow(context.Background(), "epF", rate, burst, now) // full bucket again
	if !ok {
		t.Fatal("post-flush should reconstruct a full bucket (cap-relaxing, expected)")
	}
}
