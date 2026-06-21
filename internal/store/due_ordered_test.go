//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

// TestDueOrderedHeadOnly verifies that DueDeliveryIDs returns only the
// head (cursor_seq == ordering_seq) ordered delivery plus unordered
// deliveries, skipping non-head ordered rows.
func TestDueOrderedHeadOnly(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "due.ordered.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state: cursor_seq=1, head is seq 1, backlog_count=2
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, 'key-a', 2, 1, 2)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID)
	if err != nil {
		t.Fatal(err)
	}

	// Helper to insert an ordered delivery
	insertOrderedDelivery := func(seq int64) string {
		t.Helper()
		eid := store.NewID()
		did := store.NewID()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
			 VALUES ($1, $2, 'due.ordered.created', $3, $4)`,
			eid, keyID, []byte(`{}`), 2); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
			 VALUES ($1, $2, $3, 'key-a', $4, 'pending', now(), $5)`,
			did, eid, subID, seq, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}

	did1 := insertOrderedDelivery(1) // head — should be due
	did2 := insertOrderedDelivery(2) // non-head — should NOT be due

	// Insert an unordered delivery — same subscription but NULL ordering_key/ordering_seq
	unorderedEID := store.NewID()
	unorderedDID := store.NewID()
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
		 VALUES ($1, $2, 'due.ordered.created', $3, $4)`,
		unorderedEID, keyID, []byte(`{}`), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
		 VALUES ($1, $2, $3, NULL, NULL, 'pending', now(), $4)`,
		unorderedDID, unorderedEID, subID, endpointID); err != nil {
		t.Fatal(err)
	}

	ids, err := st.DueDeliveryIDs(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}

	if !got[did1] {
		t.Error("DueDeliveryIDs missed head ordered delivery (seq 1)")
	}
	if got[did2] {
		t.Error("DueDeliveryIDs wrongly included non-head ordered delivery (seq 2)")
	}
	if !got[unorderedDID] {
		t.Error("DueDeliveryIDs missed unordered delivery")
	}
}
