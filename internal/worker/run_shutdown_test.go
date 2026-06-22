//go:build integration

package worker_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/mit112/hookrail/internal/queue"
	"github.com/mit112/hookrail/internal/worker"
)

var (
	redisOnce sync.Once
	redisAddr string
)

func testQueue(t *testing.T, group string) *queue.Queue {
	t.Helper()
	redisOnce.Do(func() {
		ctx := context.Background()
		rc, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			t.Fatalf("redis container: %v", err)
		}
		ep, err := rc.ConnectionString(ctx)
		if err != nil {
			t.Fatal(err)
		}
		redisAddr = ep
	})
	q, err := queue.New(redisAddr, "deliveries:"+t.Name(), group)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.EnsureGroup(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.Close)
	return q
}

// TestRunStopsBufferedIntakeOnSIGTERM proves the Codex M3 pre-gate BLOCKER-1
// fix: when intakeCtx is canceled (SIGTERM), Run must stop claiming NEW
// buffered work after the in-flight Process completes — it must NOT serially
// process the rest of the already-read batch (which could blow past the
// termination grace period). The unprocessed buffered deliveries stay in the
// PEL (recovered by a survivor's Autoclaim) and remain 'pending' in PG.
//
// Without the fix, Run drains its entire 16-message batch on the uncanceled
// workCtx, so every published delivery would reach a terminal state.
func TestRunStopsBufferedIntakeOnSIGTERM(t *testing.T) {
	s := testStore(t)
	q := testQueue(t, "drainers")
	ctx := context.Background()

	const n = 6
	firstHit := make(chan struct{}, 1)
	release := make(chan struct{})
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case firstHit <- struct{}{}: // signal exactly once
		default:
		}
		<-release // block until the test cancels intake
		w.WriteHeader(200)
	}))
	defer recv.Close()

	// Seed n distinct deliveries to the same receiver and publish them all so
	// a single Read returns the whole batch.
	ids := make([]string, n)
	for i := range ids {
		id, _ := seed(t, s, recv.URL)
		ids[i] = id
		if err := q.Publish(ctx, id); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	tr := worker.NewInFlight()
	w := newWorker(s)
	w.Queue = q
	w.Consumer = "drain-consumer"
	w.InFlight = tr

	intakeCtx, intakeCancel := context.WithCancel(context.Background())
	workCtx, workCancel := context.WithCancel(context.Background())
	defer workCancel()

	runDone := make(chan struct{})
	go func() { _ = w.Run(intakeCtx, workCtx); close(runDone) }()

	// The first delivery is now in-flight (handler blocked). Cancel intake
	// BEFORE releasing it, then release so Process completes and the loop
	// re-checks intakeCtx and breaks.
	select {
	case <-firstHit:
	case <-time.After(10 * time.Second):
		t.Fatal("first delivery never reached the receiver")
	}
	intakeCancel()
	close(release)

	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return promptly after intake cancel (it kept processing the buffered batch)")
	}

	// Exactly one delivery should have been processed to a terminal state;
	// the rest must still be 'pending' (never claimed — the loop broke).
	succeeded, pending := 0, 0
	for _, id := range ids {
		switch state(t, s, id) {
		case "succeeded":
			succeeded++
		case "pending":
			pending++
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded=%d, want exactly 1 (the single in-flight delivery)", succeeded)
	}
	if pending != n-1 {
		t.Fatalf("pending=%d, want %d (buffered deliveries left unprocessed on SIGTERM)", pending, n-1)
	}
}
