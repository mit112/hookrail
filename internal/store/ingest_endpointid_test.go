//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestIngestPopulatesEndpointID(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "epid.*")
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "epid.x", Payload: []byte(`{}`)})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("ingest: %v (%d deliveries)", err, len(res.DeliveryIDs))
	}
	var epID *string
	if err := s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, res.DeliveryIDs[0]).Scan(&epID); err != nil {
		t.Fatal(err)
	}
	if epID == nil || *epID == "" {
		t.Fatal("endpoint_id not populated on new delivery")
	}
}

func TestIngestSkipsSoftDeletedTargets(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "del.*")
	// soft-delete the only subscription
	if _, err := s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at = now()`); err != nil {
		t.Fatal(err)
	}
	res, err := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "del.x", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DeliveryIDs) != 0 {
		t.Fatalf("ingest created %d deliveries for a soft-deleted target, want 0", len(res.DeliveryIDs))
	}
}
