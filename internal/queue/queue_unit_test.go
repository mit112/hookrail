package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewWithClient_UsesProvidedClient(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "x:6379"})
	defer func() { _ = rdb.Close() }()
	q := NewWithClient(rdb, "s", "g")
	if q == nil || q.stream != "s" || q.group != "g" {
		t.Fatal("NewWithClient did not wire fields")
	}
	if q.MaxLen != 100_000 {
		t.Fatalf("MaxLen=%d want 100000", q.MaxLen)
	}
}

func TestIsNoGroup(t *testing.T) {
	if !IsNoGroup(errors.New("NOGROUP No such key 'hookrail:deliveries' or consumer group 'deliverers'")) {
		t.Fatal("should detect NOGROUP")
	}
	if IsNoGroup(errors.New("some other error")) {
		t.Fatal("false positive")
	}
	if IsNoGroup(nil) {
		t.Fatal("nil must be false")
	}
}

// EnsureGroupWithRetry must keep retrying (not return on the first error) until the
// bounded timeout elapses, then surface the last error — so a not-yet-converged
// Sentinel quorum at boot doesn't instantly hard-fail the daemon.
func TestEnsureGroupWithRetry_RetriesUntilTimeout(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}) // nothing listening -> fast refused
	defer func() { _ = rdb.Close() }()
	q := NewWithClient(rdb, "s", "g")
	start := time.Now()
	if err := q.EnsureGroupWithRetry(context.Background(), 1500*time.Millisecond); err == nil {
		t.Fatal("expected an error when redis is unreachable")
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("returned after %s; the ~1s retry loop was not honored", elapsed)
	}
}

// A cancelled context returns promptly with the context error (no busy spin).
func TestEnsureGroupWithRetry_ContextCancel(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() { _ = rdb.Close() }()
	q := NewWithClient(rdb, "s", "g")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.EnsureGroupWithRetry(ctx, time.Minute); err == nil {
		t.Fatal("expected context error on a cancelled context")
	}
}
