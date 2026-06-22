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

func TestTrackerPresentDuringClaimGoneAfterTerminal(t *testing.T) {
	s := testStore(t)
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

	done := make(chan struct{})
	go func() {
		w.Process(context.Background(), id)
		close(done)
	}()

	// allow time for ClaimDelivery to complete
	time.Sleep(200 * time.Millisecond)

	// BEFORE implementation: Process never calls Reserve, so this will be empty → FAIL
	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	close(proceed) // let HTTP handler return 200
	<-done         // wait for Process to finish

	// AFTER completion: terminal write was fenced and successful → Remove was called
	held = tr.DrainSnapshot(context.Background())
	if len(held) != 0 {
		t.Fatalf("expected empty tracker after completion, got %+v", held)
	}
}

func TestTrackerClearsOnNoClaim(t *testing.T) {
	s := testStore(t)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("POST happened for an already-claimed delivery")
	}))
	defer recv.Close()

	id, _ := seed(t, s, recv.URL)
	// pre-claim so Process gets no-row
	ctx := context.Background()
	if ok, _, err := s.ClaimDelivery(ctx, id, time.Minute); !ok || err != nil {
		t.Fatalf("pre-claim failed: ok=%v err=%v", ok, err)
	}

	w := newWorker(s)
	tr := worker.NewInFlight()
	w.InFlight = tr

	w.Process(ctx, id)

	// nothing was claimed under this worker, so tracker must be empty
	held := tr.DrainSnapshot(ctx)
	if len(held) != 0 {
		t.Fatalf("expected empty tracker after no-row claim, got %+v", held)
	}
}

func TestTrackerKeepsOnRecordFailure(t *testing.T) {
	s := testStore(t)
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

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		w.Process(ctx, id)
		close(done)
	}()

	// allow time for ClaimDelivery to complete
	time.Sleep(200 * time.Millisecond)

	// BEFORE implementation: Process never calls Reserve, so this will be empty → FAIL
	held := tr.DrainSnapshot(context.Background())
	if len(held) == 0 || held[0].ID != id {
		t.Fatalf("expected delivery %s in tracker snapshot, got %+v", id, held)
	}

	// Cancel the context before letting the HTTP handler proceed.
	// Process will see ctx canceled during the POST, call record with
	// a context.Canceled error (not ErrStaleClaim), and must NOT Remove
	// the tracker entry — so drain can still release it.
	cancel()
	close(proceed)
	<-done // wait for Process to finish

	// AFTER: record failed with context.Canceled → Remove must NOT have been called
	held = tr.DrainSnapshot(context.Background())
	if len(held) != 1 || held[0].ID != id {
		t.Fatalf("expected delivery %s still in tracker after failed record, got %+v", id, held)
	}
}
