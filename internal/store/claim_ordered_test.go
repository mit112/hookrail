//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// TestClaimOrderedHeadOnly verifies the head-only admission guard:
// only the delivery whose ordering_seq equals cursor_seq (the head)
// can be claimed. Non-head ordered rows are skipped.
func TestClaimOrderedHeadOnly(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=2, cursor_seq=1, backlog_count=2
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 2, 1, 2)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create two pending deliveries with ordering_key "k1", seq 1 and 2
	insertDelivery := func(seq int64) string {
		t.Helper()
		eid := store.NewID()
		did := store.NewID()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
			 VALUES ($1, $2, 'orders.created', $3, $4)`, eid, keyID, []byte(`{}`), 2); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
			 VALUES ($1, $2, $3, $4, $5, 'pending', now(), $6)`,
			did, eid, subID, "k1", seq, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}
	did1 := insertDelivery(1)
	did2 := insertDelivery(2)

	// (A) Claim non-head seq 2 while seq 1 is still pending → must fail.
	claimed, _, err := st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claimed non-head ordered delivery (seq 2) while seq 1 is pending — head-only guard missing")
	}

	// (B) Claim head seq 1 → must succeed.
	claimed, d1, err := st.ClaimDelivery(ctx, did1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("failed to claim head ordered delivery (seq 1)")
	}
	_ = d1

	// Mark seq 1 succeeded (simulating completion) and advance cursor to 2.
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET state='succeeded', lease_until=NULL WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`UPDATE ordered_key_state SET cursor_seq=2, backlog_count=1, blocked_reason=NULL, blocked_since=NULL
		 WHERE subscription_id=$1 AND ordering_key=$2`, subID, "k1"); err != nil {
		t.Fatal(err)
	}

	// (C) Now seq 2 is the head → must be claimable.
	claimed, _, err = st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("failed to claim seq 2 after head advanced to it")
	}
}
