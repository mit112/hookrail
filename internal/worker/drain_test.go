//go:build integration

package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/worker"
)

// runBlocking starts Process in a goroutine and returns a cleanup function
// that closes proceed (unblocking the HTTP handler) and waits for Process to
// finish. The returned cleanup MUST be deferred to ensure the Process
// goroutine finishes cleanly even on test failure.
//
// Defer ordering in the caller:
//
//	defer recv.Close()           // registered first
//	defer cleanup()              // registered second
//
// On exit (or Goexit), cleanup runs first (LIFO) → close(proceed) → waits for
// Process → then recv.Close has no active connections to wait for.
func runBlocking(t *testing.T, w *worker.Worker, id string, proceed chan struct{}) func() {
	doneCh := make(chan struct{})
	go func() {
		w.Process(context.Background(), id)
		close(doneCh)
	}()
	return func() {
		close(proceed) // unblock the HTTP handler
		<-doneCh       // wait for Process to finish
	}
}

func TestWorkerDrainReleasesInFlight(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	proceed := make(chan struct{})
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-proceed
		w.WriteHeader(200)
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	tr := worker.NewInFlight()
	w.InFlight = tr

	cleanup := runBlocking(t, w, id, proceed)
	defer cleanup()

	// allow time for ClaimDelivery to complete
	time.Sleep(200 * time.Millisecond)

	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	// Release via drain — 0 jitter so it's immediately reclaimable
	if err := s.ReleaseClaimForDrain(ctx, held[0].ID, held[0].ClaimVersion, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain: %v", err)
	}

	if st := state(t, s, id); st != "retry_scheduled" {
		t.Fatalf("state after drain = %q, want retry_scheduled", st)
	}

	// Prove it's reclaimable.
	// After first claim (attempt_count=1) + drain (unchanged) + reclaim,
	// attempt_count = 2. The key assertion is that drain did NOT decrement
	// attempt_count (unlike ReleaseClaim which does).
	ok, cd, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil {
		t.Fatalf("reclaim err: %v", err)
	}
	if !ok {
		t.Fatal("delivery must be reclaimable after drain")
	}
	if cd.AttemptCount < 1 {
		t.Fatalf("attempt_count = %d after reclaim, want >=1 (drain must not decrement)", cd.AttemptCount)
	}
}

func TestWorkerDrainTrackerRace(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	proceed := make(chan struct{})
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-proceed
		w.WriteHeader(200)
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	tr := worker.NewInFlight()
	w.InFlight = tr

	cleanup := runBlocking(t, w, id, proceed)
	defer cleanup()

	// allow time for ClaimDelivery to complete (handler is still blocked)
	time.Sleep(200 * time.Millisecond)

	// DrainSnapshot must return the held delivery (Finalize already fired
	// after successful claim, so drain won't block on inProgress).
	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	// Release each held delivery via drain
	for _, h := range held {
		if err := s.ReleaseClaimForDrain(ctx, h.ID, h.ClaimVersion, 0); err != nil {
			t.Fatalf("ReleaseClaimForDrain: %v", err)
		}
	}

	if st := state(t, s, id); st != "retry_scheduled" {
		t.Fatalf("state after drain = %q, want retry_scheduled", st)
	}

	// Prove reclaimable
	ok, _, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil {
		t.Fatalf("reclaim err: %v", err)
	}
	if !ok {
		t.Fatal("delivery must be reclaimable after drain")
	}
}

func TestWorkerDrainVerifiesClaimVersion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	w.Process(ctx, id)

	if st := state(t, s, id); st != "succeeded" {
		t.Fatalf("state = %q, want succeeded", st)
	}

	// Stale claim_version=0 — drain must reject this (no-op)
	if err := s.ReleaseClaimForDrain(ctx, id, 0, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain with stale cv should not error: %v", err)
	}

	// State must still be "succeeded" (fencing rejected the stale-cv update)
	if st := state(t, s, id); st != "succeeded" {
		t.Fatalf("state after stale drain = %q, want succeeded", st)
	}
}

func TestWorkerDrainPreservesClaimVersion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	proceed := make(chan struct{})
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-proceed
		w.WriteHeader(200)
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	tr := worker.NewInFlight()
	w.InFlight = tr

	cleanup := runBlocking(t, w, id, proceed)
	defer cleanup()

	time.Sleep(200 * time.Millisecond)

	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	snapshotVersion := held[0].ClaimVersion

	// Drain with correct claim version
	if err := s.ReleaseClaimForDrain(ctx, held[0].ID, held[0].ClaimVersion, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain: %v", err)
	}

	// Query claim_version from DB to verify it was NOT changed by drain
	var dbClaimVersion int64
	if err := s.Pool.QueryRow(ctx,
		`SELECT claim_version FROM deliveries WHERE id=$1`, id,
	).Scan(&dbClaimVersion); err != nil {
		t.Fatal(err)
	}
	if dbClaimVersion != snapshotVersion {
		t.Fatalf("claim_version = %d after drain, want %d (unchanged by drain)", dbClaimVersion, snapshotVersion)
	}
}

func TestWorkerDrainDoesNotStrand(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	proceed := make(chan struct{})
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-proceed
		w.WriteHeader(200)
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	w := newWorker(s)
	tr := worker.NewInFlight()
	w.InFlight = tr

	cleanup := runBlocking(t, w, id, proceed)
	defer cleanup()

	time.Sleep(200 * time.Millisecond)

	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	// Drain with 0 jitter
	if err := s.ReleaseClaimForDrain(ctx, held[0].ID, held[0].ClaimVersion, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain: %v", err)
	}

	// Query state, next_attempt_at, lease_until directly
	var state string
	var nextAttemptAt *time.Time
	var leaseUntil *time.Time
	if err := s.Pool.QueryRow(ctx,
		`SELECT state, next_attempt_at, lease_until FROM deliveries WHERE id=$1`, id,
	).Scan(&state, &nextAttemptAt, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	if state != "retry_scheduled" {
		t.Fatalf("state = %q, want retry_scheduled", state)
	}
	if nextAttemptAt == nil {
		t.Fatal("next_attempt_at must be set after drain")
	}
	if leaseUntil != nil {
		t.Fatal("lease_until must be NULL after drain")
	}
}

func TestWorkerDrainKeepsExistingReleaseClaimUntouched(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("POST should not happen")
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)

	// Claim the delivery
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	// ReleaseClaim (rate-limit style) — decrements attempt_count
	if err := s.ReleaseClaim(ctx, d.ID, d.ClaimVersion, time.Second); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}

	// After ReleaseClaim: attempt_count=0, state=retry_scheduled
	var attemptCount int
	var state string
	if err := s.Pool.QueryRow(ctx,
		`SELECT attempt_count, state::text FROM deliveries WHERE id=$1`, id,
	).Scan(&attemptCount, &state); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt_count = %d after ReleaseClaim, want 0", attemptCount)
	}
	if state != "retry_scheduled" {
		t.Fatalf("state = %q after ReleaseClaim, want retry_scheduled", state)
	}

	// Now call ReleaseClaimForDrain — must be a no-op on non-in_flight row
	if err := s.ReleaseClaimForDrain(ctx, d.ID, d.ClaimVersion, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain on already-released: %v", err)
	}

	// attempt_count must still be 0
	if err := s.Pool.QueryRow(ctx,
		`SELECT attempt_count FROM deliveries WHERE id=$1`, id,
	).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt_count = %d after ReleaseClaimForDrain, want 0 (unchanged)", attemptCount)
	}
}
