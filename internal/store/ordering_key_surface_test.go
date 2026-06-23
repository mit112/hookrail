//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// TestOrderingKeySurfacedInDLQAndBrowse proves ordering_key is exposed by the
// DLQ list, the delivery list, and the delivery timeline — non-NULL for an
// ordered delivery, NULL for the unordered path (Task 18).
func TestOrderingKeySurfacedInDLQAndBrowse(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, _, err := st.CreateProducerKey(ctx, "surface-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epOrdered, _, err := st.CreateEndpoint(ctx, masterKey(), "https://consumer.example/o", "o")
	if err != nil {
		t.Fatal(err)
	}
	epPlain, _, err := st.CreateEndpoint(ctx, masterKey(), "https://consumer.example/p", "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "orders.*", EndpointID: epOrdered, MaxAttempts: 3, Ordered: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSubscription(ctx, "payments.*", epPlain, 3); err != nil {
		t.Fatal(err)
	}

	// One ordered delivery (key "cust-1") and one unordered delivery.
	ordRes, err := st.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "orders.created", Payload: []byte(`{"n":1}`),
		OrderingKey: "cust-1", OrderedKeyBacklogMax: 10000,
	})
	if err != nil || len(ordRes.DeliveryIDs) != 1 {
		t.Fatalf("ordered ingest: %v (%d deliveries)", err, len(ordRes.DeliveryIDs))
	}
	orderedDID := ordRes.DeliveryIDs[0]

	plainRes, err := st.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "payments.captured", Payload: []byte(`{"n":2}`),
	})
	if err != nil || len(plainRes.DeliveryIDs) != 1 {
		t.Fatalf("unordered ingest: %v (%d deliveries)", err, len(plainRes.DeliveryIDs))
	}
	plainDID := plainRes.DeliveryIDs[0]

	// --- delivery browse ---
	list, err := st.ListDeliveries(ctx, store.DeliveryFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.DeliveryListRow{}
	for _, r := range list {
		byID[r.ID] = r
	}
	if got := byID[orderedDID]; got.OrderingKey == nil || *got.OrderingKey != "cust-1" {
		t.Fatalf("ordered delivery list ordering_key = %v, want cust-1", got.OrderingKey)
	}
	if got := byID[plainDID]; got.OrderingKey != nil {
		t.Fatalf("unordered delivery list ordering_key = %v, want nil", got.OrderingKey)
	}

	// --- delivery timeline ---
	otl, err := st.GetDeliveryTimeline(ctx, orderedDID)
	if err != nil {
		t.Fatal(err)
	}
	if otl.OrderingKey == nil || *otl.OrderingKey != "cust-1" {
		t.Fatalf("ordered timeline ordering_key = %v, want cust-1", otl.OrderingKey)
	}
	ptl, err := st.GetDeliveryTimeline(ctx, plainDID)
	if err != nil {
		t.Fatal(err)
	}
	if ptl.OrderingKey != nil {
		t.Fatalf("unordered timeline ordering_key = %v, want nil", ptl.OrderingKey)
	}

	// --- DLQ ---
	deadLetter(t, st, orderedDID)
	deadLetter(t, st, plainDID)
	dlq, err := st.ListDLQ(ctx, store.DLQFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	dlqByID := map[string]store.DLQRow{}
	for _, r := range dlq {
		dlqByID[r.DeliveryID] = r
	}
	if got := dlqByID[orderedDID]; got.OrderingKey == nil || *got.OrderingKey != "cust-1" {
		t.Fatalf("ordered DLQ ordering_key = %v, want cust-1", got.OrderingKey)
	}
	if got := dlqByID[plainDID]; got.OrderingKey != nil {
		t.Fatalf("unordered DLQ ordering_key = %v, want nil", got.OrderingKey)
	}
}

// deadLetter drives a delivery to dead_lettered and records a dead_letters row
// via direct SQL (no live worker needed).
func deadLetter(t *testing.T, st *store.Store, deliveryID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id, dead_at)
		 SELECT id, 'seeded', endpoint_id, $2 FROM deliveries WHERE id=$1`,
		deliveryID, time.Now()); err != nil {
		t.Fatal(err)
	}
}
