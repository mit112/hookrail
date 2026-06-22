//go:build chaos

// Package chaos integration experiments. Require a buildable compose stack + docker.
// Run via `make chaos`. Each experiment brings up a fresh stack, PROVES the fault fired,
// then polls the out-of-band oracle until invariants hold or a hard deadline (fail loud).
package chaos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

const (
	apiURL     = "http://localhost:8080"
	recvURL    = "http://localhost:9090"
	promURL    = "http://localhost:9091"
	dsn        = "postgres://hookrail:hookrail@localhost:5432/hookrail?sslmode=disable" //nolint:gosec
	succeedURL = "http://test-receiver:9090/succeed"
	slowURL    = "http://test-receiver:9090/slow-body" // holds deliveries in-flight ~5s
)

func setupStack(t *testing.T, opts ...StackOption) (*Compose, *Injector) {
	t.Helper()
	c := NewCompose()
	for _, o := range opts {
		o(c)
	}
	ctx := context.Background()
	t.Cleanup(func() {
		if err := c.Down(context.Background()); err != nil {
			t.Errorf("teardown: %v", err)
		}
		if out, _ := c.PS(context.Background()); len(trimTrailing(out)) != 0 {
			t.Errorf("containers leaked after down -v:\n%s", out)
		}
	})
	if err := c.Up(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitReady(ctx, apiURL+"/readyz"); err != nil {
		t.Fatal(err)
	}
	return c, NewInjector(c)
}

func trimTrailing(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

// E1: kill the worker while deliveries are genuinely in-flight, restart it, assert
// nothing is stranded. Recovery here is the worker's Redis PEL XAUTOCLAIM + claim
// fencing (NOT the PG sweeper — see E3 for that). /slow-body keeps deliveries in-flight
// for ~5s so the kill reliably orphans claimed work (Codex folds #2,#5).
func TestExperimentWorkerCrash(t *testing.T) {
	c, inj := setupStack(t)
	ctx := context.Background()

	key, err := Seed(ctx, c, slowURL, "chaos-e1.*")
	if err != nil {
		t.Fatal(err)
	}
	load := &Load{APIURL: apiURL, Key: key, Topic: "chaos-e1.evt"}

	const n = 200
	accepted, err := load.Burst(ctx, n)
	if err != nil || accepted != n {
		t.Fatalf("burst accepted %d/%d: %v", accepted, n, err)
	}

	// Prove the fault has live work to disrupt: ≥1 delivery in-flight BEFORE the kill.
	pre, err := WaitNonTerminal(ctx, dsn, 1, 30*time.Second)
	if err != nil {
		t.Fatalf("no in-flight work to disrupt (vacuous): %v", err)
	}
	inFlightAtKill := pre.NonTerminal()
	t.Logf("fault fired: killing worker with %d deliveries non-terminal", inFlightAtKill)

	if err := inj.Kill(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := inj.Start(ctx, "worker"); err != nil {
		t.Fatal(err)
	}

	// Only deliveries claimed-but-unacked at the kill can legitimately be re-sent.
	dupBound := inFlightAtKill + 5
	snap, err := PollRecovered(ctx, recvURL, dsn, accepted, dupBound, 4*time.Minute)
	if err != nil {
		t.Fatalf("not recovered: %v", err)
	}
	t.Logf("recovered (PEL): succeeded=%d distinct=%d dups=%d (bound %d)", snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, dupBound)
}

// E2: pause Postgres under sustained load. Ingress must fail CLOSED within the per-request
// timeout (never hang — Codex fold #3), and after unpause everything drains to zero
// stranded. dupBound is the in-flight count captured during the pause (Codex fold #7).
func TestExperimentPostgresOutage(t *testing.T) {
	c, inj := setupStack(t)
	ctx := context.Background()

	key, err := Seed(ctx, c, slowURL, "chaos-e2.*")
	if err != nil {
		t.Fatal(err)
	}
	load := &Load{APIURL: apiURL, Key: key, Topic: "chaos-e2.evt"}

	stop := load.Steady(ctx, 40)
	if _, err := WaitNonTerminal(ctx, dsn, 1, 30*time.Second); err != nil {
		t.Fatalf("no traffic established: %v", err)
	}

	if err := inj.Pause(ctx, "postgres"); err != nil {
		t.Fatal(err)
	}
	duringCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	during, _ := FetchDB(duringCtx, dsn) // best-effort; may timeout while paused
	// Ingress must fail closed within the load client's 3s timeout, not hang.
	postDone := make(chan error, 1)
	go func() { _, e := load.Post(ctx); postDone <- e }()
	select {
	case e := <-postDone:
		if e == nil {
			t.Fatal("expected ingest failure while postgres paused (fail-closed)")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("ingest hung while postgres paused (no fail-closed within timeout)")
	}
	time.Sleep(10 * time.Second)
	if err := inj.Unpause(ctx, "postgres"); err != nil {
		t.Fatal(err)
	}

	accepted, attempts := stop()
	if accepted == 0 {
		t.Fatal("no events accepted — load driver never ran")
	}
	dupBound := during.NonTerminal() + 10
	// Ingress-path fault: a post in-flight at the pause boundary can time out client-side
	// (counted rejected, so excluded from accepted) yet still commit when Postgres unpauses,
	// delivering exactly once. So succeeded may exceed accepted, bounded above by every post
	// ever attempted (Steady attempts + the one explicit fail-closed probe) — you cannot
	// deliver an event that was never posted. during.NonTerminal() cannot supply this bound:
	// FetchDB ran while PG was paused and timed out to zero.
	upper := attempts + 1
	t.Logf("fault fired: postgres paused 10s; accepted=%d attempts=%d upper=%d dupBound=%d", accepted, attempts, upper, dupBound)
	snap, err := PollRecoveredBounded(ctx, recvURL, dsn, accepted, upper, dupBound, 4*time.Minute)
	if err != nil {
		t.Fatalf("not recovered: %v", err)
	}
	t.Logf("recovered: accepted=%d succeeded=%d distinct=%d dups=%d", accepted, snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates)
}

// E3: wipe Redis (stream + group) and restart the consumers; Postgres is the source of
// truth, so the PG sweeper must republish the surviving rows. Asserts the sweeper counter
// rises (observable PG-recovery) AND everything delivers.
func TestExperimentRedisQueueLoss(t *testing.T) {
	c, inj := setupStack(t)
	_ = inj
	ctx := context.Background()

	key, err := Seed(ctx, c, slowURL, "chaos-e3.*")
	if err != nil {
		t.Fatal(err)
	}
	load := &Load{APIURL: apiURL, Key: key, Topic: "chaos-e3.evt"}

	const n = 200
	accepted, err := load.Burst(ctx, n)
	if err != nil || accepted != n {
		t.Fatalf("burst accepted %d/%d: %v", accepted, n, err)
	}
	// Ensure there is surviving non-terminal work in PG to republish.
	pre, err := WaitNonTerminal(ctx, dsn, 1, 30*time.Second)
	if err != nil {
		t.Fatalf("no surviving work for the sweeper (vacuous): %v", err)
	}
	base, _ := FetchCounter(ctx, promURL, "hookrail_sweeper_republished_total")
	t.Logf("fault: FLUSHALL with %d non-terminal in PG; sweeper base=%v", pre.NonTerminal(), base)

	if out, err := c.Exec(ctx, "redis", "redis-cli", "FLUSHALL"); err != nil {
		t.Fatalf("FLUSHALL: %v\n%s", err, out)
	}
	if err := c.Restart(ctx, "worker", "scheduler"); err != nil { // recreate the consumer group
		t.Fatal(err)
	}

	// The PG sweeper must republish the surviving rows — prove the counter rises.
	if _, err := WaitCounterAbove(ctx, promURL, "hookrail_sweeper_republished_total", base, 3*time.Minute); err != nil {
		t.Fatalf("PG sweeper never republished (PG-as-source-of-truth unproven): %v", err)
	}
	dupBound := pre.NonTerminal() + 10
	snap, err := PollRecovered(ctx, recvURL, dsn, accepted, dupBound, 5*time.Minute)
	if err != nil {
		t.Fatalf("not recovered: %v", err)
	}
	t.Logf("recovered via PG sweeper: succeeded=%d distinct=%d dups=%d", snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates)
}

// E5: ordered FIFO under worker kill — the zero-reorder oracle.
func TestExperimentOrderedNoReorder(t *testing.T) {
	c, inj := setupStack(t)
	ctx := context.Background()

	const orderedURL = "http://test-receiver:9090/ordered-slow"
	key, err := SeedOrdered(ctx, c, orderedURL, "chaos-e5.*")
	if err != nil {
		t.Fatal(err)
	}
	load := &Load{APIURL: apiURL, Key: key, Topic: "chaos-e5.evt"}

	const K = 3  // distinct ordering keys
	const M = 30 // events per key

	// Post K*M events, sequence 1..M per key, interleaved.
	var totalAccepted int
	for seq := 1; seq <= M; seq++ {
		for ki := 0; ki < K; ki++ {
			orderingKey := fmt.Sprintf("key-%d", ki)
			ids, err := load.PostOrdered(ctx, orderingKey, seq)
			if err != nil {
				t.Fatalf("post ordered key=%s seq=%d: %v", orderingKey, seq, err)
			}
			totalAccepted += len(ids)
		}
	}
	t.Logf("accepted %d deliveries across %d keys × %d events", totalAccepted, K, M)

	// Prove the fault has a live ordered delivery IN-FLIGHT to disrupt — not merely
	// pending backlog. With at-most-one-in-flight per key, NonTerminal is dominated by
	// pending rows, so we must assert in_flight>=1 specifically; otherwise the kill only
	// proves drain-after-restart, not recovery of a killed in-flight ordered head.
	pre, err := WaitInFlight(ctx, dsn, 1, 30*time.Second)
	if err != nil {
		t.Fatalf("no ordered delivery in-flight to disrupt (vacuous): %v", err)
	}
	t.Logf("fault fired: killing worker with %d in-flight (%d non-terminal)", pre.InFlight, pre.NonTerminal())

	if err := inj.Kill(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if err := inj.Start(ctx, "worker"); err != nil {
		t.Fatal(err)
	}

	// Only claimed-but-unacked deliveries at the kill can legitimately be re-sent; bound
	// dups by the in-flight count (≤K) plus slack, not the (much larger) pending backlog.
	dupBound := pre.InFlight + 5
	snap, err := PollRecovered(ctx, recvURL, dsn, totalAccepted, dupBound, 4*time.Minute)
	if err != nil {
		t.Fatalf("not recovered: %v", err)
	}
	t.Logf("recovered: succeeded=%d distinct=%d dups=%d (bound %d)",
		snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, dupBound)

	// Ordered oracle: poll per-key arrival order, assert strictly monotonic.
	var orderedStats map[string][]int
	for attempt := 0; attempt < 15; attempt++ {
		orderedStats = fetchOrderedStats(ctx, recvURL)
		if len(orderedStats) == K {
			complete := true
			for ki := 0; ki < K; ki++ {
				if len(orderedStats[fmt.Sprintf("key-%d", ki)]) != M {
					complete = false
					break
				}
			}
			if complete {
				break
			}
		}
		time.Sleep(time.Second)
	}
	if len(orderedStats) != K {
		t.Fatalf("ordered keys: got %d, want %d: %v", len(orderedStats), K, orderedStats)
	}
	for ki := 0; ki < K; ki++ {
		keyName := fmt.Sprintf("key-%d", ki)
		seqs, ok := orderedStats[keyName]
		if !ok {
			t.Fatalf("key %q missing from ordered-stats", keyName)
		}
		if len(seqs) != M {
			t.Fatalf("key %q: got %d seqs, want %d: %v", keyName, len(seqs), M, seqs)
		}
		// Strictly increasing: no reorder, no gaps, no dupes.
		seen := map[int]bool{}
		for i, s := range seqs {
			if s < 1 || s > M {
				t.Fatalf("key %q: seq %d out of range [1,%d] at position %d", keyName, s, M, i)
			}
			if seen[s] {
				t.Fatalf("key %q: duplicate seq %d at position %d", keyName, s, i)
			}
			seen[s] = true
			if i > 0 && s <= seqs[i-1] {
				t.Fatalf("key %q: reorder at position %d: %d after %d", keyName, i, s, seqs[i-1])
			}
		}
		for s := 1; s <= M; s++ {
			if !seen[s] {
				t.Fatalf("key %q: missing seq %d (gap)", keyName, s)
			}
		}
	}
	t.Logf("ordered oracle: %d keys × %d seqs all strictly monotonic, no gaps, no dupes", K, M)
}

// TestHarnessScaleAndKillLeader validates the harness primitives that E6 depends on:
// compose scale (multiple scheduler replicas) and leader-targeted SIGKILL.
func TestHarnessScaleAndKillLeader(t *testing.T) {
	c := NewCompose()
	ctx := context.Background()
	t.Cleanup(func() {
		if err := c.Down(context.Background()); err != nil {
			t.Errorf("teardown: %v", err)
		}
		if out, _ := c.PS(context.Background()); len(trimTrailing(out)) != 0 {
			t.Errorf("containers leaked after down -v:\n%s", out)
		}
	})
	// Bring up scheduler=2 so we have a leader + standby to fail over.
	if err := c.Up(ctx, map[string]int{"scheduler": 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitReady(ctx, apiURL+"/readyz"); err != nil {
		t.Fatal(err)
	}
	inj := NewInjector(c)

	// Kill the single leader container (fails loudly on split brain / no leader);
	// a DIFFERENT surviving container must report leader shortly after, scraped
	// directly (not via aggregate Prometheus, which can serve a stale sample).
	killed, err := inj.KillLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newLeader, err := inj.WaitNewLeader(ctx, killed, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("failover: killed leader %.12s, survivor %.12s promoted", killed, newLeader)
}

// E6: leader scheduler failover — kill the leader while a SWEEPER-ONLY backlog
// is outstanding; a surviving scheduler must promote and continue republishing
// it, with nothing stranded.
//
// Non-vacuity (the credibility, per the chaos-oracle lesson): we use /fail/2 so
// every delivery returns 500 on its first two attempts before succeeding. A
// failed attempt moves the delivery to retry_scheduled with a future
// next_attempt_at; the ingest hot-path XADD already happened once and was
// consumed+ACKed on that failed attempt, so a due RETRY is re-published to Redis
// ONLY by the scheduler sweeper. That makes the outstanding work a genuine
// sweeper-only backlog — exactly what a dead leader must hand off. Workers stay
// up the whole time, so recovery cannot be attributed to PEL autoclaim or
// hot-path entries: if the survivor does not sweep, the retries never reach
// attempt 3 and PollRecovered times out.
func TestExperimentLeaderFailover(t *testing.T) {
	c := NewCompose()
	ctx := context.Background()
	t.Cleanup(func() {
		if err := c.Down(context.Background()); err != nil {
			t.Errorf("teardown: %v", err)
		}
		if out, _ := c.PS(context.Background()); len(trimTrailing(out)) != 0 {
			t.Errorf("containers leaked after down -v:\n%s", out)
		}
	})

	// Bring up scheduler=2 (leader + standby), worker=2.
	if err := c.Up(ctx, map[string]int{"scheduler": 2, "worker": 2}); err != nil {
		t.Fatal(err)
	}
	if err := c.WaitReady(ctx, apiURL+"/readyz"); err != nil {
		t.Fatal(err)
	}
	inj := NewInjector(c)

	// /fail/2: each delivery 500s on attempts 1 and 2, then 200 (seed sets
	// max_attempts=8, so /fail/2 never dead-letters).
	key, err := Seed(ctx, c, "http://test-receiver:9090/fail/2", "chaos-e6.*")
	if err != nil {
		t.Fatal(err)
	}
	load := &Load{APIURL: apiURL, Key: key, Topic: "chaos-e6.evt"}

	const n = 200
	accepted, err := load.Burst(ctx, n)
	if err != nil || accepted != n {
		t.Fatalf("burst accepted %d/%d: %v", accepted, n, err)
	}

	// Wait for a real retry backlog: deliveries that failed at least once and are
	// now retry_scheduled, republishable ONLY by the sweeper. Nothing has
	// succeeded yet (each needs attempt 3), so this is genuinely sweeper-only work.
	var preDB DBState
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		d, e := FetchDB(ctx, dsn)
		if e == nil && d.RetryScheduled >= 50 {
			preDB = d
			break
		}
		time.Sleep(time.Second)
	}
	if preDB.RetryScheduled < 50 {
		t.Fatalf("no sweeper-only retry backlog formed (vacuous): retry_scheduled=%d", preDB.RetryScheduled)
	}
	t.Logf("pre-kill: retry_scheduled=%d in-flight=%d succeeded=%d", preDB.RetryScheduled, preDB.InFlight, preDB.Succeeded)

	// Kill the single leader scheduler (fails loudly on split brain / no leader).
	killed, err := inj.KillLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// (a) A DIFFERENT surviving scheduler promotes — scraped directly from the
	// container, so a stale killed-leader sample can't fake it.
	newLeader, err := inj.WaitNewLeader(ctx, killed, 30*time.Second)
	if err != nil {
		t.Fatalf("no survivor promoted after KillLeader: %v", err)
	}
	t.Logf("failover: killed %.12s, survivor %.12s promoted", killed, newLeader)

	// (b) Baseline captured AFTER the old leader is dead and the survivor is
	// confirmed leader, so any rise is unambiguously the survivor's republish
	// work (not a pre-kill sweep by the doomed leader).
	base, _ := FetchCounter(ctx, promURL, "hookrail_sweeper_republished_total")
	if _, err := WaitCounterAbove(ctx, promURL, "hookrail_sweeper_republished_total", base, 3*time.Minute); err != nil {
		t.Fatalf("survivor never republished the sweeper-only backlog post-failover: %v", err)
	}

	// (c)+(d) Every accepted delivery eventually succeeds (each after 2 failures
	// driven through the survivor's sweep), nothing stranded; duplicates bounded
	// by the in-flight count at kill time + slack (workers are never killed here,
	// so dups stay near zero).
	dupBound := preDB.InFlight + 10
	snap, err := PollRecovered(ctx, recvURL, dsn, accepted, dupBound, 6*time.Minute)
	if err != nil {
		t.Fatalf("not recovered: %v", err)
	}
	t.Logf("recovered (leader failover): succeeded=%d distinct=%d dups=%d (bound %d)",
		snap.DB.Succeeded, snap.Stats.Distinct, snap.Stats.Duplicates, dupBound)
}

// E_RL: degrade the dedicated limiter connection while the queue path stays
// healthy. Assert fail-open liveness (deliveries continue, mode=failopen
// appears) and cap re-enforcement after recovery.
func TestChaos_E_RL_LimiterFailOpenAndRecover(t *testing.T) {
	c, _ := setupStack(t,
		WithScale("worker", 2),
		WithEnv("HOOKRAIL_RL_REDIS_ADDR", "toxiproxy:8479"),
		WithEnv("HOOKRAIL_LIMITS_REFRESH_INTERVAL", "1s"),
	)
	ctx := context.Background()

	// Wait for toxiproxy to be ready, then create the limiter proxy
	// so the dedicated limiter client (pointed at toxiproxy:8479)
	// can reach Redis through it.
	if err := c.WaitReady(ctx, toxiproxyURL+"/proxies"); err != nil {
		t.Fatalf("toxiproxy not ready: %v", err)
	}
	if err := setupLimiterProxy(ctx); err != nil {
		t.Fatalf("setup limiter proxy: %v", err)
	}

	// Seed a rate-limited endpoint (20 rps override).
	sr, err := SeedWithRPS(ctx, c, 20.0, slowURL, "chaos-rl.*")
	if err != nil {
		t.Fatalf("seed with rps: %v", err)
	}
	load := &Load{APIURL: apiURL, Key: sr.ProducerKey, Topic: "chaos-rl.evt"}

	// Saturate demand so the cap is genuinely exercised.
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for i := 0; i < 4; i++ {
		go func() {
			for sctx.Err() == nil {
				_, _ = load.Post(sctx)
			}
		}()
	}

	// Prove the global cap is active and denying before we cut.
	if _, err := waitDenials(ctx, "global", 30*time.Second); err != nil {
		t.Fatalf("global cap never engaged: %v", err)
	}

	// Cut the limiter connection (queue Redis stays healthy).
	if err := cutLimiter(ctx); err != nil {
		t.Fatalf("cut limiter: %v", err)
	}

	// Liveness: deliveries must keep GENUINELY flowing while the limiter is
	// down — measured by real delivery_attempts for the seeded endpoint, NOT
	// deliveries.total (which rises from continuous ingress alone even if every
	// worker stalled). Fail-open is cap-relaxing, so attempts in the window must
	// exceed what the global cap by itself would have admitted.
	const rate = 20.0
	const windowSec = 5.0
	cutAttempts, err := countAttemptsInWindow(ctx, sr.EndpointID, 5*time.Second)
	if err != nil {
		t.Fatalf("count attempts during cut: %v", err)
	}
	if float64(cutAttempts) <= rate*windowSec {
		t.Fatalf("fail-open liveness: deliveries must continue (cap-relaxing) while limiter is down; got %d attempts in %.0fs, expected > %.0f", cutAttempts, windowSec, rate*windowSec)
	}
	t.Logf("fail-open liveness during cut: attempts=%d (> global-cap window %.0f)", cutAttempts, rate*windowSec)

	// Prove mode=failopen is active during the cut.
	m, err := scrapeRatelimit(ctx)
	if err != nil {
		t.Fatalf("scrape ratelimit: %v", err)
	}
	if m.FailOpen == 0 {
		t.Fatal("expected mode=failopen during the cut")
	}
	t.Logf("cut active: failopen=%d deniedGlobal=%d", m.FailOpen, m.DeniedGlobal)

	// Restore the limiter connection.
	if err := restoreLimiter(ctx); err != nil {
		t.Fatalf("restore limiter: %v", err)
	}

	// Wait for recovery: breaker cooldown (15s) + refresh interval (1s).
	time.Sleep(20 * time.Second)

	// Recovery: failopen must stop growing.
	fo1, err := failopenCount(ctx)
	if err != nil {
		t.Fatalf("failopen count after recovery: %v", err)
	}
	time.Sleep(5 * time.Second)
	fo2, err := failopenCount(ctx)
	if err != nil {
		t.Fatalf("failopen count: %v", err)
	}
	if fo2 != fo1 {
		t.Fatalf("failopen must stop growing after recovery: %d -> %d", fo1, fo2)
	}
	t.Logf("recovery: failopen stable at %d", fo1)

	// Cap re-enforced: a fresh saturated window must be BOUNDED ABOVE (cap
	// holds), BOUNDED BELOW (deliveries still flow — not a stalled worker), and
	// the global limiter must be actively DENYING again (denied(global) grows),
	// proving the cap is genuinely binding rather than throughput merely being
	// low. Snapshot the global-denied counter around the window.
	preDenied, err := scrapeRatelimit(ctx)
	if err != nil {
		t.Fatalf("scrape denied before recovery window: %v", err)
	}
	admitted, err := countAttemptsInWindow(ctx, sr.EndpointID, 5*time.Second)
	if err != nil {
		t.Fatalf("count window: %v", err)
	}
	postDenied, err := scrapeRatelimit(ctx)
	if err != nil {
		t.Fatalf("scrape denied after recovery window: %v", err)
	}
	const burst = 40.0      // 2*rps
	const epsilon = 15      // async metric/attempt accounting slack across 2 workers
	lower := rate           // >= ~1s of admitted throughput: deliveries still flow
	upper := rate*windowSec + burst + epsilon
	if float64(admitted) < lower {
		t.Fatalf("recovery: delivery path stalled, admitted=%d < lower=%.0f", admitted, lower)
	}
	if float64(admitted) > upper {
		t.Fatalf("cap not re-enforced after recovery: admitted=%d > upper=%.0f", admitted, upper)
	}
	if postDenied.DeniedGlobal <= preDenied.DeniedGlobal {
		t.Fatalf("global cap not re-engaged after recovery: denied(global) did not grow (%d -> %d)", preDenied.DeniedGlobal, postDenied.DeniedGlobal)
	}
	t.Logf("cap re-enforced: admitted=%d in [%.0f,%.0f]; denied(global) grew %d -> %d", admitted, lower, upper, preDenied.DeniedGlobal, postDenied.DeniedGlobal)
}

// fetchOrderedStats calls GET /ordered-stats on the test-receiver.
func fetchOrderedStats(ctx context.Context, recvURL string) map[string][]int {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, recvURL+"/ordered-stats", nil)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string][]int
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	return out
}
