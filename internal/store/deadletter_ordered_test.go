//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// TestDeadLetterExhaustedOrderedBlocks verifies that DeadLetterExhausted on
// the ordered head sets blocked_reason='dead_lettered' and blocked_since,
// cursor stays at the blocked head, the head delivery is dead_lettered, and
// the successor is NOT claimable (crash-loop path through Terminal helper).
func TestDeadLetterExhaustedOrderedBlocks(t *testing.T) {
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

	insertDelivery := func(seq int64, state string) string {
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
			 VALUES ($1, $2, $3, $4, $5, $6::delivery_state, now(), $7)`,
			did, eid, subID, "k1", seq, state, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}
	did1 := insertDelivery(1, "in_flight")
	did2 := insertDelivery(2, "pending")

	// Set claim_version=1 on did1 so the fence matches
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET claim_version=1 WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}

	// Dead-letter exhausted (crash-loop path) → should block, cursor unchanged
	nextHead, err := st.DeadLetterExhausted(ctx, did1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead != nil {
		t.Fatalf("DeadLetterExhausted returned nextHead=%s, want nil (blocked)", *nextHead)
	}

	// Verify cursor stays at 1 (blocked by dead-lettered head)
	var cursor int64
	var blockedReason *string
	var blockedSince *time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason, blocked_since FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason, &blockedSince); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 {
		t.Fatalf("cursor_seq = %d, want 1 (still blocked)", cursor)
	}
	if blockedReason == nil || *blockedReason != "dead_lettered" {
		t.Fatalf("blocked_reason = %v, want 'dead_lettered'", blockedReason)
	}
	if blockedSince == nil {
		t.Fatal("blocked_since is nil, want non-nil")
	}

	// Verify did1 state is dead_lettered
	var state string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "dead_lettered" {
		t.Fatalf("did1 state = %s, want dead_lettered", state)
	}

	// Seq 2 must NOT be claimable (key is blocked by dead-lettered head)
	claimed, _, err := st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("claimed non-head delivery (seq 2) while head is dead-lettered — admission guard broken")
	}
}

// TestDeadLetterExhaustedOrderedIdempotent verifies that calling
// DeadLetterExhausted on an already-dead-lettered delivery (row not in_flight)
// returns ErrStaleClaim and does not change blocked_reason.
func TestDeadLetterExhaustedOrderedIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=1, cursor_seq=1,
	// already blocked with dead_lettered
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count,
		 blocked_reason, blocked_since)
		 VALUES ($1, $2, 1, 1, 1, 'dead_lettered', now())
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create one delivery already dead_lettered with claim_version=5
	eid := store.NewID()
	did := store.NewID()
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
		 VALUES ($1, $2, 'orders.created', $3, $4)`, eid, keyID, []byte(`{}`), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id, claim_version)
		 VALUES ($1, $2, $3, $4, $5, 'dead_lettered', now(), $6, 5)`,
		did, eid, subID, "k1", int64(1), endpointID); err != nil {
		t.Fatal(err)
	}

	// Call DeadLetterExhausted with claim_version=5 — delivery is already
	// dead_lettered (not in_flight) so the fence should reject it.
	_, err = st.DeadLetterExhausted(ctx, did, 5)
	if !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("DeadLetterExhausted on dead_lettered row: got %v, want ErrStaleClaim", err)
	}

	// Verify blocked_reason is still "dead_lettered" (unchanged by stale call)
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT blocked_reason FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&blockedReason); err != nil {
		t.Fatal(err)
	}
	if blockedReason == nil || *blockedReason != "dead_lettered" {
		t.Fatalf("blocked_reason = %v, want 'dead_lettered' after stale DeadLetterExhausted", blockedReason)
	}
}
