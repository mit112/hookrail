//go:build integration

package integration

import (
	"context"
	"testing"
	"time"
)

const (
	rlRPS    = 20.0            // per-endpoint override used by every case
	rlBurst  = 2 * rlRPS       // EndpointLimits sets burst = 2*rps
	rlWindow = 8 * time.Second // steady-state measurement window
	epsilon  = 10              // slack for async metric/attempt accounting
	fastTick = "1s"            // shrink the limits-refresh so the seeded override propagates fast
)

// measure runs a saturated steady-state window and returns the number of
// delivery attempts admitted during [t0,t1) plus the window length in seconds.
// A continuous publisher keeps demand far above the cap for the whole window,
// so the count reflects the true admit rate and can never be vacuously zero.
func measure(ctx context.Context, t *testing.T, ep string) (int, float64) {
	t.Helper()
	t0, err := DBNow(ctx)
	if err != nil {
		t.Fatalf("db now (t0): %v", err)
	}
	time.Sleep(rlWindow)
	t1, err := DBNow(ctx)
	if err != nil {
		t.Fatalf("db now (t1): %v", err)
	}
	admitted, err := CountAttemptsBetween(ctx, ep, t0, t1)
	if err != nil {
		t.Fatalf("count attempts: %v", err)
	}
	return admitted, t1.Sub(t0).Seconds()
}

// TestGlobalCap_TwoWorkers_BoundHolds: with 2 workers sharing ONE Redis token
// bucket at 20 rps, total admitted across both workers stays at the cluster cap
// (rate*window + burst). Non-vacuity is enforced from both sides:
//   - upper bound: admitted <= rate*window + burst + epsilon (cap holds)
//   - lower bound: admitted >= 0.4*rate*window (the window genuinely saw
//     sustained delivery — a zero/near-zero count FAILS instead of passing)
//   - deniedGlobal > 0 (the limiter actually denied, summed over BOTH workers)
//   - failopen == 0 (healthy run, summed over BOTH workers)
func TestGlobalCap_TwoWorkers_BoundHolds(t *testing.T) {
	c := SetupStack(t, WithWorkerScale(2), WithEnv("HOOKRAIL_LIMITS_REFRESH_INTERVAL", fastTick))
	ctx := context.Background()

	if err := WaitWorkerTargets(ctx, 2, 60*time.Second); err != nil {
		t.Fatalf("worker targets: %v", err)
	}
	sr, err := SeedWithRPS(ctx, c, rlRPS)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ep := sr.EndpointID

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	(&Load{APIURL: apiURL, Key: sr.ProducerKey, Topic: "rl-test.evt"}).StartSaturation(sctx, 4)

	// Wait until the global cap is actually active and denying before measuring.
	if _, err := WaitDenials(ctx, "global", 30*time.Second); err != nil {
		t.Fatalf("global cap never engaged: %v", err)
	}

	admitted, window := measure(ctx, t, ep)
	upper := rlRPS*window + rlBurst + epsilon
	lower := 0.4 * rlRPS * window
	t.Logf("admitted=%d window=%.1fs upper=%.0f lower=%.0f", admitted, window, upper, lower)

	if float64(admitted) > upper {
		t.Fatalf("global cap breached: admitted=%d > upper=%.0f", admitted, upper)
	}
	if float64(admitted) < lower {
		t.Fatalf("admitted=%d < lower=%.0f — window not saturated; oracle would be vacuous", admitted, lower)
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
	t.Logf("deniedGlobal=%d failopen=%d", m.DeniedGlobal, m.FailOpen)
}

// TestGlobalCap_FlagOff_ExceedsBound: the control. With HOOKRAIL_GLOBAL_RATELIMIT=0,
// each of the 2 workers enforces the 20 rps override in its OWN local bucket, so
// aggregate throughput is ~2x the single-node cap. This proves the two-worker
// test above is bounded BECAUSE of the global limiter, not because demand was
// insufficient. deniedLocal>0 proves the override actually propagated to the
// local registry (otherwise the default 1000 rps would let it pass for the wrong
// reason).
func TestGlobalCap_FlagOff_ExceedsBound(t *testing.T) {
	c := SetupStack(t,
		WithWorkerScale(2),
		WithEnv("HOOKRAIL_GLOBAL_RATELIMIT", "0"),
		WithEnv("HOOKRAIL_LIMITS_REFRESH_INTERVAL", fastTick),
	)
	ctx := context.Background()

	if err := WaitWorkerTargets(ctx, 2, 60*time.Second); err != nil {
		t.Fatalf("worker targets: %v", err)
	}
	sr, err := SeedWithRPS(ctx, c, rlRPS)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	ep := sr.EndpointID

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	(&Load{APIURL: apiURL, Key: sr.ProducerKey, Topic: "rl-test.evt"}).StartSaturation(sctx, 4)

	// Wait until local rate-limiting is active+denying (proves the override
	// reached the local registry — not the 1000 rps default).
	if _, err := WaitDenials(ctx, "local", 30*time.Second); err != nil {
		t.Fatalf("local cap never engaged (override did not propagate): %v", err)
	}

	admitted, window := measure(ctx, t, ep)
	singleNode := rlRPS*window + rlBurst
	twoNode := 2*(rlRPS*window+rlBurst) + epsilon
	t.Logf("admitted=%d window=%.1fs singleNode=%.0f twoNode=%.0f", admitted, window, singleNode, twoNode)

	if float64(admitted) <= singleNode {
		t.Fatalf("flag-off should exceed the single-node bound (~2x); admitted=%d <= %.0f", admitted, singleNode)
	}
	if float64(admitted) > twoNode {
		t.Fatalf("flag-off exceeded even the 2-worker bound; admitted=%d > %.0f", admitted, twoNode)
	}

	m, err := ScrapeRatelimit(ctx)
	if err != nil {
		t.Fatalf("scrape ratelimit: %v", err)
	}
	if m.DeniedLocal == 0 {
		t.Fatal("denied(local) must be > 0 — the override did not reach the local registry")
	}
	if m.DeniedGlobal != 0 {
		t.Fatalf("global limiter must be inactive when flag is off, got deniedGlobal=%d", m.DeniedGlobal)
	}
	t.Logf("deniedLocal=%d deniedGlobal=%d", m.DeniedLocal, m.DeniedGlobal)
}
