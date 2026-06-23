//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// TestRetentionKeepsBlockingHeadPayload proves the ordered-keys retention
// invariant (N9/§D9): the payload of a delivery that is the dead_lettered HEAD
// of a still-blocked key is NEVER tombstoned, even when the event is old and its
// dead-letter is too stale to be protected by the replayable-DLQ guard. Once the
// head is skipped (the key unblocks), normal retention tombstones it (Task 20).
func TestRetentionKeepsBlockingHeadPayload(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, _, err := st.CreateProducerKey(ctx, "ret-ord-test", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	epID, _, err := st.CreateEndpoint(ctx, masterKey(), "https://consumer.example/h", "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSubscriptionFull(ctx, store.SubInput{
		TopicPattern: "retord.*", EndpointID: epID, MaxAttempts: 3, Ordered: true,
	}); err != nil {
		t.Fatal(err)
	}
	var subID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM subscriptions LIMIT 1`).Scan(&subID); err != nil {
		t.Fatal(err)
	}

	res, err := st.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID, Topic: "retord.x", Payload: []byte(`{"big":"payload"}`),
		OrderingKey: "k1", OrderedKeyBacklogMax: 10000,
	})
	if err != nil || len(res.DeliveryIDs) != 1 {
		t.Fatalf("ingest: %v", err)
	}
	did := res.DeliveryIDs[0]
	var eid string
	if err := st.Pool.QueryRow(ctx, `SELECT event_id FROM deliveries WHERE id=$1`, did).Scan(&eid); err != nil {
		t.Fatal(err)
	}

	// Age the event past retention, dead-letter the head, and record a STALE
	// dead-letter (dead_at 60d ago) so the existing replayable-DLQ guard does
	// NOT protect the payload — only the ordered-blocking invariant can.
	if _, err := st.Pool.Exec(ctx, `UPDATE events SET created_at = now() - interval '60 days' WHERE id=$1`, eid); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE deliveries SET state='dead_lettered', lease_until=NULL WHERE id=$1`, did); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id, dead_at, replayed_at)
		 SELECT id, 'x', endpoint_id, now() - interval '60 days', NULL FROM deliveries WHERE id=$1`, did); err != nil {
		t.Fatal(err)
	}
	// Recompute so ordered_key_state marks the key blocked with head=did.
	mustRecompute(t, st, subID, "k1")

	// Tombstone pass: the blocking head's payload MUST survive.
	if _, err := st.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000); err != nil {
		t.Fatal(err)
	}
	var size int
	if err := st.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size == 0 {
		t.Fatal("tombstoned a blocking dead_lettered head's payload — N9/§D9 violation")
	}

	// Skip the head → the key unblocks and the delivery is no longer the head.
	if _, err := st.SkipHead(ctx, did, "test", "manual skip"); err != nil {
		t.Fatalf("SkipHead: %v", err)
	}

	// Now normal retention applies — the old skipped payload is tombstoned.
	if _, err := st.TombstoneEventPayloads(ctx, 30*24*time.Hour, 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT payload_size FROM events WHERE id=$1`, eid).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		t.Fatalf("payload not tombstoned after skip unblocked the key: size=%d", size)
	}
}

func mustRecompute(t *testing.T, st *store.Store, subID, key string) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
		subID, key); err != nil {
		t.Fatal(err)
	}
	if err := st.RecomputeCursor(ctx, tx, subID, key); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
