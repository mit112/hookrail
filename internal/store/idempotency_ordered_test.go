//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestIdempotencyOrderedReplay(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _ := seedOrderedPipeline(t, s, "orders.*")

	params := store.IngestParams{
		ProducerKeyID:       keyID,
		Topic:               "orders.created",
		Payload:             []byte(`{"n":1}`),
		IdemKey:             "ordered-replay-idem",
		IdemTTL:             24 * time.Hour,
		OrderingKey:         "cust-42",
		OrderedKeyBacklogMax: 10000,
	}

	res1, err := s.IngestEvent(ctx, params)
	if err != nil {
		t.Fatal(err)
	}

	res2, err := s.IngestEvent(ctx, params)
	if err != nil {
		t.Fatal(err)
	}

	if !res2.Replayed {
		t.Fatal("second ingest with same params + ordering key: want Replayed=true, got false")
	}
	if res2.EventID != res1.EventID {
		t.Fatalf("replay returned event %s, want %s", res2.EventID, res1.EventID)
	}
	if len(res2.DeliveryIDs) != 1 {
		t.Fatalf("replay returned %d deliveries, want 1", len(res2.DeliveryIDs))
	}
}

func TestIdempotencyOrderedKeyConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _ := seedOrderedPipeline(t, s, "orders.*")

	first := store.IngestParams{
		ProducerKeyID:       keyID,
		Topic:               "orders.created",
		Payload:             []byte(`{"n":1}`),
		IdemKey:             "ordered-conflict-idem",
		IdemTTL:             24 * time.Hour,
		OrderingKey:         "cust-A",
		OrderedKeyBacklogMax: 10000,
	}

	_, err := s.IngestEvent(ctx, first)
	if err != nil {
		t.Fatal(err)
	}

	second := first
	second.OrderingKey = "cust-B"
	_, err = s.IngestEvent(ctx, second)
	if !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("different ordering key with same idem key: want ErrIdempotencyConflict, got %v", err)
	}
}
