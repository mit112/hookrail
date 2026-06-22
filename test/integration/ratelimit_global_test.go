//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"
)

const epsilon = 10 // slack for async metrics polling

// TestGlobalCap_TwoWorkers_BoundHolds verifies that with 2 workers and a 20 rps
// override, the global token bucket limits total admitted to ≤ burst + rate×window.
// failopen must be 0 (healthy run) and denied(global) > 0 (non-vacuous).
func TestGlobalCap_TwoWorkers_BoundHolds(t *testing.T) {
	c := SetupStack(t, WithWorkerScale(2))
	ctx := context.Background()

	sr, err := SeedWithRPS(ctx, c, 20.0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ep := sr.EndpointID

	load := &Load{APIURL: apiURL, Key: sr.ProducerKey, Topic: "rl-test.*"}
	_ = load.Burst(ctx, 100) // warm up
	time.Sleep(2 * time.Second)

	// Publish saturating burst.
	_ = load.Burst(ctx, 2000)
	time.Sleep(10 * time.Second)

	admitted, err := CountAttemptsInWindow(ctx, ep, 10*time.Second)
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	bound := 20.0*10 + 40 + epsilon
	if float64(admitted) > bound {
		t.Fatalf("global cap breached: %d > %.0f", admitted, bound)
	}

	m, err := ScrapeRatelimit(ctx)
	if err != nil {
		t.Fatalf("scrape ratelimit: %v", err)
	}
	if m.FailOpen != 0 {
		t.Fatalf("failopen must be 0 in a healthy run, got %d", m.FailOpen)
	}
	if m.DeniedGlobal == 0 {
		t.Fatal("denied(global) must be > 0 or the cap never bound (vacuous)")
	}
	t.Logf("admitted=%d bound=%.0f deniedGlobal=%d failopen=%d", admitted, bound, m.DeniedGlobal, m.FailOpen)
}

// TestGlobalCap_FlagOff_ExceedsBound verifies that with HOOKRAIL_GLOBAL_RATELIMIT=0
// and 2 workers, admitted exceeds the single-node bound (each worker has its own
// local bucket, giving ~2× the capacity).
func TestGlobalCap_FlagOff_ExceedsBound(t *testing.T) {
	c := SetupStack(t, WithWorkerScale(2), WithEnv("HOOKRAIL_GLOBAL_RATELIMIT", "0"))
	ctx := context.Background()

	sr, err := SeedWithRPS(ctx, c, 20.0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ep := sr.EndpointID

	load := &Load{APIURL: apiURL, Key: sr.ProducerKey, Topic: "rl-test.*"}
	_ = load.Burst(ctx, 100) // warm up
	time.Sleep(2 * time.Second)

	_ = load.Burst(ctx, 2000)
	time.Sleep(10 * time.Second)

	admitted, err := CountAttemptsInWindow(ctx, ep, 10*time.Second)
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	singleNodeBound := 20.0*10 + 40
	if float64(admitted) <= singleNodeBound {
		t.Fatalf("flag-off should exceed the single-node bound (~2×); got %d <= %.0f", admitted, singleNodeBound)
	}
	t.Logf("admitted=%d singleNodeBound=%.0f (flag-off exceeds bound as expected)", admitted, singleNodeBound)
}

func TestGlobalCap_Disabled_NoConfigAgeMetric(t *testing.T) {
	c := SetupStack(t, WithWorkerScale(1), WithEnv("HOOKRAIL_GLOBAL_RATELIMIT", "0"))
	ctx := context.Background()

	_, err := SeedWithRPS(ctx, c, 5.0)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Give the worker time to start and tick.
	time.Sleep(5 * time.Second)

	// When global is disabled, the config-age metric should stay at 0 (never set).
	v, err := fetchGauge(ctx, promURL, "hookrail_ratelimit_config_age_seconds")
	// Prometheus returns nothing for a metric that was never registered, so
	// fetchGauge may error or return 0.
	if err == nil && v > 0 {
		t.Fatalf("config-age metric should be 0 when global rate limit is disabled, got %v", v)
	}
	_ = fmt.Sprintf("config-age metric: %v (err=%v)", v, err) // for logs
}
