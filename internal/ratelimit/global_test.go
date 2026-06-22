package ratelimit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// errLimiter always returns an error, simulating a limiter-command failure.
type errLimiter struct{}

func (errLimiter) Allow(ctx context.Context, key string, rate, burst float64, now time.Time) (bool, error) {
	return false, errors.New("boom")
}

// okLimiter always allows (used for race tests).
type okLimiter struct{}

func (okLimiter) Allow(ctx context.Context, key string, rate, burst float64, now time.Time) (bool, error) {
	return true, nil
}

func TestGlobalLimiter_FailOpenUsesFallback(t *testing.T) {
	fb := NewRegistry(1000, 2000)
	g := NewGlobalLimiterWith(errLimiter{}, fb, 50*time.Millisecond)
	g.SetSnapshot(map[string]Limit{"ep1": {Rate: 10, Burst: 20}})
	ok, mode := g.Allow(context.Background(), "ep1", time.Now())
	if !ok || mode != "failopen" {
		t.Fatalf("want allow+failopen, got ok=%v mode=%s", ok, mode)
	}
}

func TestGlobalLimiter_SnapshotSwapRace(t *testing.T) {
	g := NewGlobalLimiterWith(okLimiter{}, NewRegistry(1, 1), time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			g.SetSnapshot(map[string]Limit{"e": {Rate: 1, Burst: 1}})
		}()
		go func() {
			defer wg.Done()
			g.Allow(context.Background(), "e", time.Now())
		}()
	}
	wg.Wait()
}
