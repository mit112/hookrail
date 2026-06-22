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
	dsn        = "postgres://hookrail:hookrail@localhost:5432/hookrail?sslmode=disable"
	succeedURL = "http://test-receiver:9090/succeed"
	slowURL    = "http://test-receiver:9090/slow-body" // holds deliveries in-flight ~5s
)

func setupStack(t *testing.T) (*Compose, *Injector) {
	t.Helper()
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

	const K = 3   // distinct ordering keys
	const M = 30  // events per key

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

	// Kill the leader container; the standby must report leader shortly after.
	if err := inj.KillLeader(ctx); err != nil {
		t.Fatal(err)
	}

	// Prove the standby promoted: is_leader gauge reads 1 within a bound.
	deadline := time.Now().Add(30 * time.Second)
	promQuery := "hookrail_scheduler_is_leader"
	for time.Now().Before(deadline) {
		v, err := FetchCounter(ctx, promURL, promQuery)
		if err == nil && v >= 1.0 {
			t.Logf("standby promoted: is_leader=%v", v)
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("standby scheduler never became leader after KillLeader within 30s")
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
