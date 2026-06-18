//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestDeleteEndpointCancelsPendingAndRetry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cx.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cx.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	var epID string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, did).Scan(&epID)

	if err := s.SoftDeleteEndpoint(ctx, epID); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("pending delivery state after endpoint delete = %q, want cancelled", state)
	}
}

func TestDeleteLeavesInFlight(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cy.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cy.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	var epID string
	_ = s.Pool.QueryRow(ctx, `SELECT endpoint_id FROM deliveries WHERE id=$1`, did).Scan(&epID)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='in_flight', lease_until=now()+interval '30s' WHERE id=$1`, did)

	_ = s.SoftDeleteEndpoint(ctx, epID)
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "in_flight" {
		t.Fatalf("in_flight delivery state = %q, want in_flight (left to finish)", state)
	}
}

func TestCancelOrphanedReconciles(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID := seedPipeline(t, s, "cz.*")
	res, _ := s.IngestEvent(ctx, store.IngestParams{ProducerKeyID: keyID, Topic: "cz.x", Payload: []byte(`{}`)})
	did := res.DeliveryIDs[0]
	// straggler: sub soft-deleted but delivery left retry_scheduled (race window)
	_, _ = s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now()`)
	_, _ = s.Pool.Exec(ctx, `UPDATE deliveries SET state='retry_scheduled' WHERE id=$1`, did)
	n, err := s.CancelOrphaned(ctx, 100)
	if err != nil || n != 1 {
		t.Fatalf("CancelOrphaned = %d, %v; want 1", n, err)
	}
	var state string
	_ = s.Pool.QueryRow(ctx, `SELECT state::text FROM deliveries WHERE id=$1`, did).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("orphan state = %q, want cancelled", state)
	}
}
