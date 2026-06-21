//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/store"
)

// TestReplayOrderedUnblocks verifies that replaying a dead-lettered ordered
// head clears blocked_reason and blocked_since, keeps cursor at the head
// (which is now pending), returns the head id so the admin handler can
// republish it, and the head becomes claimable again.  After the head
// succeeds, the cursor advances to the next delivery.
func TestReplayOrderedUnblocks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=2, cursor=1, backlog=2,
	// already blocked with dead_lettered.
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count,
		 blocked_reason, blocked_since)
		 VALUES ($1, $2, 2, 1, 2, 'dead_lettered', now())
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create events and deliveries for seq 1 (dead_lettered) and seq 2 (pending).
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
		if state == "dead_lettered" {
			if _, err := st.Pool.Exec(ctx,
				`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id, dead_at)
				 VALUES ($1, 'permanent', $2, now())`, did, endpointID); err != nil {
				t.Fatal(err)
			}
		}
		return did
	}
	did1 := insertDelivery(1, "dead_lettered")
	did2 := insertDelivery(2, "pending")

	// Verify initial state: cursor=1, blocked
	var cursor int64
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 || blockedReason == nil || *blockedReason != "dead_lettered" {
		t.Fatalf("initial: cursor=%d blocked=%v, want cursor=1 blocked='dead_lettered'", cursor, blockedReason)
	}

	// Seq 2 must NOT be claimable while head is dead_lettered
	claimed, _, err := st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("seq 2 claimable while head dead_lettered — admission guard broken")
	}

	// Replay the blocked head.
	out, nextHead, err := st.ReplayDeadLetter(ctx, did1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if out != store.ReplayOK {
		t.Fatalf("replay outcome = %v, want ReplayOK", out)
	}
	if nextHead == nil {
		t.Fatal("ReplayDeadLetter returned nil head, want did1 (re-armed head)")
	}
	if *nextHead != did1 {
		t.Fatalf("nextHead = %s, want %s (re-armed head)", *nextHead, did1)
	}

	// blocked_reason and blocked_since must be cleared.
	var blockedSince *time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason, blocked_since FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason, &blockedSince); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 {
		t.Fatalf("cursor after replay = %d, want 1 (head is now pending)", cursor)
	}
	if blockedReason != nil {
		t.Fatalf("blocked_reason after replay = %v, want nil", blockedReason)
	}
	if blockedSince != nil {
		t.Fatalf("blocked_since after replay = %v, want nil", blockedSince)
	}

	// Delivery state must be pending.
	var state string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Fatalf("did1 state after replay = %s, want pending", state)
	}

	// Now claim and complete the head → cursor should advance to 2.
	ok, dClaim, err := st.ClaimDelivery(ctx, did1, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after replay: ok=%v err=%v", ok, err)
	}

	res := store.AttemptResult{
		DeliveryID: did1, AttemptNo: 1, ClaimVersion: dClaim.ClaimVersion,
		Outcome: domain.OutcomeSuccess, HTTPStatus: 200,
		LatencyMS: 5, RequestedAt: time.Now().Add(-5 * time.Millisecond), CompletedAt: time.Now(),
	}
	advHead, err := st.CompleteAttempt(ctx, res, backoff.Default(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if advHead == nil || *advHead != did2 {
		t.Fatalf("CompleteAttempt nextHead = %v, want %s", advHead, did2)
	}

	// Cursor must advance to 2.
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("cursor after success = %d, want 2", cursor)
	}

	// Seq 2 is now claimable.
	claimed2, _, err := st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed2 {
		t.Fatal("seq 2 not claimable after cursor advanced")
	}
}
