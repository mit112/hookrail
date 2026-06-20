//go:build chaos

// Package chaos integration experiments. Require a buildable compose stack + docker.
// Run via `make chaos`. Each experiment brings up a fresh stack, PROVES the fault fired,
// then polls the out-of-band oracle until invariants hold or a hard deadline (fail loud).
package chaos

import (
	"context"
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
	if err := c.Up(ctx); err != nil {
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
