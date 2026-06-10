//go:build integration

package store_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// mkDelivery seeds one pending delivery and returns its id.
func mkDelivery(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := context.Background()
	keyID := seedPipeline(t, s, "claims.*")
	res, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "claims.test", Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryIDs) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(res.DeliveryIDs))
	}
	return res.DeliveryIDs[0]
}

func setState(t *testing.T, s *store.Store, id, state string, leaseUntil time.Time) {
	t.Helper()
	_, err := s.Pool.Exec(context.Background(),
		`UPDATE deliveries SET state = $2::delivery_state, lease_until = $3 WHERE id = $1`,
		id, state, leaseUntil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestClaimRaceExactlyOneWinner(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	const workers = 8
	wins := make([]bool, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claimed, _, err := s.ClaimDelivery(context.Background(), id, 30*time.Second)
			if err != nil {
				t.Errorf("worker %d: %v", i, err)
				return
			}
			wins[i] = claimed
		}(i)
	}
	wg.Wait()
	n := 0
	for _, w := range wins {
		if w {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d workers claimed the same delivery, want exactly 1", n)
	}
}

func TestClaimRespectsActiveLease(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	if ok, _, _ := s.ClaimDelivery(context.Background(), id, 30*time.Second); !ok {
		t.Fatal("first claim failed")
	}
	// second claim while the lease is live must lose
	if ok, _, _ := s.ClaimDelivery(context.Background(), id, 30*time.Second); ok {
		t.Fatal("claimed a delivery with a live lease")
	}
}

func TestLeaseTakeoverAfterExpiry(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	// simulate a crashed worker: in_flight with an expired lease (§10 row 1)
	// the worker had claimed it once, so attempt_count was 1 before the crash
	setState(t, s, id, "in_flight", time.Now().Add(-time.Minute))
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE deliveries SET attempt_count = 1 WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	claimed, d, err := s.ClaimDelivery(context.Background(), id, 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("takeover of expired lease failed: claimed=%v err=%v", claimed, err)
	}
	if d.AttemptCount < 2 {
		t.Fatalf("attempt_count = %d after takeover, want >= 2 (claim increments)", d.AttemptCount)
	}
}

func TestTerminalStatesNotClaimable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, st := range []string{"succeeded", "dead_lettered"} {
		// use a unique pattern per iteration so seedPipeline does not accumulate
		// matching subscriptions across iterations (which would multiply deliveries)
		keyID := seedPipeline(t, s, "claims."+st)
		res, err := s.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID: keyID,
			Topic:         "claims." + st,
			Payload:       []byte(`{}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.DeliveryIDs) != 1 {
			t.Fatalf("want 1 delivery, got %d", len(res.DeliveryIDs))
		}
		id := res.DeliveryIDs[0]
		setState(t, s, id, st, time.Time{})
		if ok, _, _ := s.ClaimDelivery(context.Background(), id, 30*time.Second); ok {
			t.Fatalf("claimed a %s delivery", st)
		}
	}
}

func TestNotDueDeliveryNotClaimable(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	// duplicate Redis message for a freshly rescheduled delivery:
	// retry_scheduled with a future next_attempt_at must NOT be claimable —
	// otherwise duplicates bypass the backoff schedule (§4)
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE deliveries SET state='retry_scheduled', next_attempt_at = now() + interval '1 hour' WHERE id=$1`,
		id); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.ClaimDelivery(context.Background(), id, 30*time.Second); ok {
		t.Fatal("claimed a retry_scheduled delivery before its next_attempt_at")
	}
}

func TestReleaseClaimRestoresAttemptBudget(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	ok, d, err := s.ClaimDelivery(context.Background(), id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := s.ReleaseClaim(context.Background(), d.ID, d.ClaimVersion, 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT attempt_count FROM deliveries WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("attempt_count = %d after release, want 0", count)
	}
	// immediately reclaimable (release used delay 0)
	if ok, d2, _ := s.ClaimDelivery(context.Background(), id, 30*time.Second); !ok || d2.AttemptCount != 1 {
		t.Fatalf("reclaim after release: ok=%v count=%d, want true/1", ok, d2.AttemptCount)
	}
}
