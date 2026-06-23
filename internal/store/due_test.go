//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// mkDueDelivery creates a delivery with a unique subscription pattern so
// each call produces exactly 1 delivery regardless of call order.
func mkDueDelivery(t *testing.T, s *store.Store, label string) string {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "due-test-"+label, []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "due-"+label)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubscription(ctx, "due."+label, epID, 8); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "due." + label, Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryIDs) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(res.DeliveryIDs))
	}
	return res.DeliveryIDs[0]
}

func TestDueDeliveryIDs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	duePending := mkDueDelivery(t, s, "a") // pending, next_attempt_at = now() → due
	dueRetry := mkDueDelivery(t, s, "b")
	setState(t, s, dueRetry, "retry_scheduled", time.Time{})
	stuck := mkDueDelivery(t, s, "c")
	setState(t, s, stuck, "in_flight", time.Now().Add(-time.Minute)) // expired lease → stuck

	futureRetry := mkDueDelivery(t, s, "d")
	if _, err := s.Pool.Exec(ctx,
		`UPDATE deliveries SET state='retry_scheduled', next_attempt_at = now() + interval '1 hour' WHERE id=$1`,
		futureRetry); err != nil {
		t.Fatal(err)
	}
	leased := mkDueDelivery(t, s, "e")
	setState(t, s, leased, "in_flight", time.Now().Add(time.Minute)) // live lease → owned
	done := mkDueDelivery(t, s, "f")
	setState(t, s, done, "succeeded", time.Time{})

	ids, err := s.DueDeliveryIDs(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{duePending, dueRetry, stuck} {
		if !got[want] {
			t.Errorf("due scan missed %s", want)
		}
	}
	for _, no := range []string{futureRetry, leased, done} {
		if got[no] {
			t.Errorf("due scan wrongly included %s", no)
		}
	}
}