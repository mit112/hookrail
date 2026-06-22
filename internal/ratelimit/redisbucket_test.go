package ratelimit

import (
	"context"
	"sync"
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
