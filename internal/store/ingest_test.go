//go:build integration

package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// seedPipeline creates key + endpoint + N subscriptions on the given topic pattern.
func seedPipeline(t *testing.T, s *store.Store, patterns ...string) (keyID string) {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "ingest-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range patterns {
		epID, _, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "t")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateSubscription(ctx, p, epID, 8); err != nil {
			t.Fatal(err)
		}
	}
	return keyID
}

func TestIngestFanOut(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*", "orders.created", "payments.*")
	res, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":1}`), IdemTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	// orders.* and orders.created match; payments.* does not
	if len(res.DeliveryIDs) != 2 {
		t.Fatalf("fan-out created %d deliveries, want 2", len(res.DeliveryIDs))
	}
	if res.Replayed {
		t.Fatal("fresh ingest marked replayed")
	}
}

func TestIngestIdempotentReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")
	p := store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":2}`), IdemKey: "idem-replay-1", IdemTTL: 24 * time.Hour,
	}
	first, err := s.IngestEvent(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.IngestEvent(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || second.EventID != first.EventID {
		t.Fatalf("replay: got (id=%s replayed=%v), want (id=%s replayed=true)",
			second.EventID, second.Replayed, first.EventID)
	}
	// exactly one event row and one fan-out — no duplicates
	var n int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM deliveries WHERE event_id = $1`, first.EventID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("deliveries for event = %d (err %v), want 1", n, err)
	}
}

func TestIngestIdempotencyConflict409(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")
	p := store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":3}`), IdemKey: "idem-conflict-1", IdemTTL: 24 * time.Hour,
	}
	if _, err := s.IngestEvent(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Payload = []byte(`{"n":"DIFFERENT"}`) // same key, different body → 409 (§4)
	if _, err := s.IngestEvent(ctx, p); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
}

func TestIngestSameKeySamePayloadDifferentTopic409(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*", "billing.*")
	p := store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":7}`), IdemKey: "idem-topic-1", IdemTTL: 24 * time.Hour,
	}
	if _, err := s.IngestEvent(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Topic = "billing.created" // same key+payload, different topic → conflict, not replay
	if _, err := s.IngestEvent(ctx, p); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict for topic change, got %v", err)
	}
}

func TestIngestExpiredIdemKeyIsReusable(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")
	p := store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":8}`), IdemKey: "idem-expired-1",
		IdemTTL: -time.Hour, // already expired on insert
	}
	first, err := s.IngestEvent(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	p.IdemTTL = 24 * time.Hour
	second, err := s.IngestEvent(ctx, p) // must take over the expired row
	if err != nil {
		t.Fatalf("reuse of expired key failed: %v", err)
	}
	if second.Replayed || second.EventID == first.EventID {
		t.Fatalf("expired key replayed old event %s", first.EventID)
	}
}

func TestIngestConcurrentDuplicates(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")
	p := store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created",
		Payload: []byte(`{"n":4}`), IdemKey: "idem-race-1", IdemTTL: 24 * time.Hour,
	}
	const n = 8
	results := make([]store.IngestResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.IngestEvent(ctx, p)
		}(i)
	}
	wg.Wait()
	eventIDs := map[string]bool{}
	replays := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		eventIDs[results[i].EventID] = true
		if results[i].Replayed {
			replays++
		}
	}
	if len(eventIDs) != 1 {
		t.Fatalf("concurrent duplicates created %d distinct events, want 1", len(eventIDs))
	}
	if replays != n-1 {
		t.Fatalf("%d replays, want %d (exactly one winner)", replays, n-1)
	}
	var rows int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE topic='orders.created' AND payload @> '{"n":4}'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("event rows = %d (err %v), want 1", rows, err)
	}
}

func TestIngestNoIdemKeyCreatesDistinctEvents(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")
	p := store.IngestParams{ProducerKeyID: keyID, Topic: "orders.created", Payload: []byte(`{"n":5}`)}
	a, _ := s.IngestEvent(ctx, p)
	b, _ := s.IngestEvent(ctx, p)
	if a.EventID == b.EventID {
		t.Fatal("no idempotency key but events deduped")
	}
}
