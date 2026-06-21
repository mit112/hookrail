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

// TestCompleteAttemptOrderedAdvancesCursor verifies that completing the
// ordered head (seq 1) advances the cursor to the next delivery (seq 2)
// and returns the new head id for the worker to publish.
func TestCompleteAttemptOrderedAdvancesCursor(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=2, cursor=1, backlog=2
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 2, 1, 2)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create events and deliveries for seq 1 (in_flight) and seq 2 (pending)
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

	// Set claim_version=1 on did1 so the CompleteAttempt fence matches
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET claim_version=1 WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}

	// Complete did1 successfully → should return did2 as next head
	res := store.AttemptResult{
		DeliveryID: did1, AttemptNo: 1, ClaimVersion: 1,
		Outcome: domain.OutcomeSuccess, HTTPStatus: 200,
		LatencyMS: 12, RequestedAt: time.Now().Add(-12 * time.Millisecond), CompletedAt: time.Now(),
	}
	nextHead, err := st.CompleteAttempt(ctx, res, backoff.Default(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead == nil {
		t.Fatal("CompleteAttempt on ordered delivery returned nil head, want did2")
	}
	if *nextHead != did2 {
		t.Fatalf("nextHead = %s, want %s", *nextHead, did2)
	}

	// Verify cursor advanced from 1 to 2
	var cursor int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("cursor_seq = %d, want 2", cursor)
	}

	// Verify did1 state is succeeded
	var state string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("did1 state = %s, want succeeded", state)
	}

	// Verify did2 is still pending and claimable as the new head
	var st2 string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did2).Scan(&st2); err != nil {
		t.Fatal(err)
	}
	if st2 != "pending" {
		t.Fatalf("did2 state = %s, want pending", st2)
	}
}

// TestCompleteAttemptOrderedDeadLetterBlocks verifies that a dead-lettered
// ordered head sets blocked_reason='dead_lettered' and cursor stays at the
// blocked head (successor NOT returned as head).
func TestCompleteAttemptOrderedDeadLetterBlocks(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": seq_counter=2, cursor=1
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
	_ = insertDelivery(2, "pending") // seq 2 is waiting

	// Set claim_version=1 on did1
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET claim_version=1 WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}

	// Permanent failure → dead_lettered → should block, not advance cursor
	res := store.AttemptResult{
		DeliveryID: did1, AttemptNo: 1, ClaimVersion: 1,
		Outcome: domain.OutcomePermanent, HTTPStatus: 500, ErrorClass: "http_500",
		LatencyMS: 12, RequestedAt: time.Now().Add(-12 * time.Millisecond), CompletedAt: time.Now(),
	}
	nextHead, err := st.CompleteAttempt(ctx, res, backoff.Default(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead != nil {
		t.Fatalf("dead-lettered head returned nextHead=%s, want nil (blocked)", *nextHead)
	}

	// Verify cursor stays at 1 (blocked by dead-lettered head)
	var cursor int64
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 {
		t.Fatalf("cursor_seq = %d, want 1 (still blocked)", cursor)
	}
	if blockedReason == nil || *blockedReason != "dead_lettered" {
		t.Fatalf("blocked_reason = %v, want 'dead_lettered'", blockedReason)
	}

	// Seq 2 must NOT be claimable (head blocked)
	claimed, _, err := st.ClaimDelivery(ctx, did1, 30*time.Second) // did1 is now dead_lettered
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("dead_lettered head was claimable — admission guard broken")
	}
}

// TestCompleteAttemptUnorderedUnaffected verifies the unordered path
// returns nil head and behaves exactly as before.
func TestCompleteAttemptUnorderedUnaffected(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)

	nextHead, err := s.CompleteAttempt(context.Background(),
		result(d, domain.OutcomeSuccess, 200, ""), backoff.Default(), d.MaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead != nil {
		t.Fatalf("unordered CompleteAttempt returned head=%s, want nil", *nextHead)
	}
	st, _ := deliveryState(t, s, d.ID)
	if st != "succeeded" {
		t.Fatalf("state = %s, want succeeded", st)
	}
}
