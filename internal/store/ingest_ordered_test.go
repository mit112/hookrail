//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func seedOrderedPipeline(t *testing.T, s *store.Store, topicPattern string) (keyID, subID string) {
	t.Helper()
	ctx := context.Background()
	keyID, _, err := s.CreateProducerKey(ctx, "ordered-ingest-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "t")
	if err != nil {
		t.Fatal(err)
	}
	subID = store.NewID()
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO subscriptions (id, topic_pattern, endpoint_id, max_attempts, ordered)
		 VALUES ($1, $2, $3, 8, true)`, subID, topicPattern, epID)
	if err != nil {
		t.Fatal(err)
	}
	return keyID, subID
}

func TestIngestOrderedAssignsSeq(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _ := seedOrderedPipeline(t, s, "orders.*")

	res1, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID:       keyID,
		Topic:               "orders.created",
		Payload:             []byte(`{"n":1}`),
		IdemTTL:             24 * time.Hour,
		OrderingKey:         "cust-42",
		OrderedKeyBacklogMax: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.DeliveryIDs) != 1 {
		t.Fatalf("fan-out created %d deliveries, want 1", len(res1.DeliveryIDs))
	}
	var okey string
	var oseq int64
	if err := s.Pool.QueryRow(ctx,
		`SELECT ordering_key, ordering_seq FROM deliveries WHERE id = $1`,
		res1.DeliveryIDs[0]).Scan(&okey, &oseq); err != nil {
		t.Fatal(err)
	}
	if okey != "cust-42" {
		t.Fatalf("ordering_key = %q, want cust-42", okey)
	}
	if oseq != 1 {
		t.Fatalf("ordering_seq = %d, want 1", oseq)
	}

	res2, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID:       keyID,
		Topic:               "orders.created",
		Payload:             []byte(`{"n":2}`),
		IdemTTL:             24 * time.Hour,
		OrderingKey:         "cust-42",
		OrderedKeyBacklogMax: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx,
		`SELECT ordering_seq FROM deliveries WHERE id = $1`,
		res2.DeliveryIDs[0]).Scan(&oseq); err != nil {
		t.Fatal(err)
	}
	if oseq != 2 {
		t.Fatalf("second ordering_seq = %d, want 2", oseq)
	}

	if len(res1.PublishableIDs) != 1 || res1.PublishableIDs[0] != res1.DeliveryIDs[0] {
		t.Fatalf("first delivery not publishable: PublishableIDs=%v", res1.PublishableIDs)
	}
	if len(res2.PublishableIDs) != 0 {
		t.Fatalf("non-head delivery published: PublishableIDs=%v", res2.PublishableIDs)
	}
}

func TestIngestOrderedBacklogCap(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, _ := seedOrderedPipeline(t, s, "orders.*")

	cap := 3
	for i := 0; i < cap; i++ {
		_, err := s.IngestEvent(ctx, store.IngestParams{
			ProducerKeyID:       keyID,
			Topic:               "orders.created",
			Payload:             []byte(`{"n":1}`),
			IdemTTL:             24 * time.Hour,
			OrderingKey:         "cust-backlog",
			OrderedKeyBacklogMax: cap,
		})
		if err != nil {
			t.Fatalf("ingest %d: %v", i+1, err)
		}
	}

	_, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID:       keyID,
		Topic:               "orders.created",
		Payload:             []byte(`{"n":2}`),
		IdemTTL:             24 * time.Hour,
		OrderingKey:         "cust-backlog",
		OrderedKeyBacklogMax: cap,
	})
	if !errors.Is(err, store.ErrBacklogFull) {
		t.Fatalf("want ErrBacklogFull, got %v", err)
	}
}

func TestIngestOrderedUnorderedUnaffected(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "orders.*")

	res, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID,
		Topic:         "orders.created",
		Payload:       []byte(`{"n":1}`),
		IdemTTL:       24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryIDs) != 1 {
		t.Fatalf("unordered delivery count = %d, want 1", len(res.DeliveryIDs))
	}
	if len(res.PublishableIDs) != 1 || res.PublishableIDs[0] != res.DeliveryIDs[0] {
		t.Fatalf("unordered delivery not publishable: PublishableIDs=%v", res.PublishableIDs)
	}
	var okey *string
	var oseq *int64
	if err := s.Pool.QueryRow(ctx,
		`SELECT ordering_key, ordering_seq FROM deliveries WHERE id = $1`,
		res.DeliveryIDs[0]).Scan(&okey, &oseq); err != nil {
		t.Fatal(err)
	}
	if okey != nil {
		t.Fatalf("unordered delivery has ordering_key = %v", *okey)
	}
	if oseq != nil {
		t.Fatalf("unordered delivery has ordering_seq = %v", *oseq)
	}
}
