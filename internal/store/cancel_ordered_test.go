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

// TestCancelOrderedHeadAdvances — case (a): cancel head seq1 → cursor advances
func TestCancelOrderedHeadAdvances(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state: seq_counter=2, cursor=1, backlog=2
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
	did1 := insertDelivery(1, "pending")
	did2 := insertDelivery(2, "pending")

	// Verify initial cursor=1
	var cursor int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 {
		t.Fatalf("initial cursor = %d, want 1", cursor)
	}

	// SoftDeleteSubscription cancels all pending deliveries.
	if err := st.SoftDeleteSubscription(ctx, subID); err != nil {
		t.Fatal(err)
	}

	// Both deliveries must be cancelled.
	var state string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did1).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Fatalf("did1 state = %s, want cancelled", state)
	}
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, did2).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Fatalf("did2 state = %s, want cancelled", state)
	}

	// Cursor must advance to sentinel = seq_counter+1 = 3.
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 3 {
		t.Fatalf("cursor after cancel head = %d, want 3", cursor)
	}
	if blockedReason != nil {
		t.Fatalf("blocked_reason = %v, want nil", blockedReason)
	}
}

// TestCancelOrderedSuccessorDoesNotAdvance — case (b): cancel successor seq2
// while head seq1 still pending → cursor stays at 1.
func TestCancelOrderedSuccessorDoesNotAdvance(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state: seq_counter=2, cursor=1, backlog=2
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
	did1 := insertDelivery(1, "pending")
	did2 := insertDelivery(2, "pending")

	// Cancel seq2 only (raw SQL — targeted).
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET state='cancelled' WHERE id=$1`, did2); err != nil {
		t.Fatal(err)
	}

	// Recompute via ApplyOrderedTerminal — seq1 still blocks so cursor stays at 1.
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	headID, err := st.ApplyOrderedTerminal(ctx, tx, subID, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var cursor int64
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 1 {
		t.Fatalf("cursor after successor cancel = %d, want 1", cursor)
	}
	if headID == nil || *headID != did1 {
		t.Fatalf("headID = %v, want %s", headID, did1)
	}

	// Seq1 must still be claimable.
	claimed, _, err := st.ClaimDelivery(ctx, did1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("seq1 not claimable after successor cancel")
	}
}

// TestCancelOrderedSkipsAfterHeadTerminal — case (c): complete head seq1,
// then cancel seq2 → cursor skips to seq3/sentinel.
func TestCancelOrderedSkipsAfterHeadTerminal(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state: seq_counter=3, cursor=1, backlog=3
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 3, 1, 3)
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
	did3 := insertDelivery(3, "pending")

	// Set claim_version=1 on did1
	if _, err := st.Pool.Exec(ctx,
		`UPDATE deliveries SET claim_version=1 WHERE id=$1`, did1); err != nil {
		t.Fatal(err)
	}

	// Complete seq1 → cursor to 2.
	res := store.AttemptResult{
		DeliveryID: did1, AttemptNo: 1, ClaimVersion: 1,
		Outcome: domain.OutcomeSuccess, HTTPStatus: 200,
		LatencyMS: 5, RequestedAt: time.Now().Add(-5 * time.Millisecond), CompletedAt: time.Now(),
	}
	nextHead, err := st.CompleteAttempt(ctx, res, backoff.Default(), 8)
	if err != nil {
		t.Fatal(err)
	}
	if nextHead == nil || *nextHead != did2 {
		t.Fatalf("CompleteAttempt nextHead = %v, want %s", nextHead, did2)
	}

	// Verify cursor at 2
	var cursor int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("cursor after seq1 success = %d, want 2", cursor)
	}

	// SoftDeleteSubscription cancels seq2+seq3
	if err := st.SoftDeleteSubscription(ctx, subID); err != nil {
		t.Fatal(err)
	}

	// Seq2 and seq3 must be cancelled
	for _, did := range []string{did2, did3} {
		var state string
		if err := st.Pool.QueryRow(ctx,
			`SELECT state FROM deliveries WHERE id=$1`, did).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != "cancelled" {
			t.Fatalf("%s state = %s, want cancelled", did, state)
		}
	}

	// Cursor → sentinel = seq_counter+1 = 4
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 4 {
		t.Fatalf("cursor after cancel all = %d, want 4", cursor)
	}
	if blockedReason != nil {
		t.Fatalf("blocked_reason = %v, want nil", blockedReason)
	}
}

// TestCancelOrphanedRecomputesJustCancelledKey is the BLOCKER-2 regression guard.
// cancelOrphanedTx cancels a batch-bounded set of orphan rows, then must recompute
// EXACTLY the keys it just cancelled. The old code re-derived the keys with a
// separate `SELECT DISTINCT ... ORDER BY subscription_id, ordering_key LIMIT batch`,
// so a pre-existing cancelled key sorting earlier ("aaa") could fill the LIMIT and
// shadow the key actually cancelled this pass ("zzz") — leaving its cursor parked
// on the now-cancelled head forever. Here batch=1: the only pending orphan is on
// key "zzz", but "aaa" already has a cancelled row that sorts first.
func TestCancelOrphanedRecomputesJustCancelledKey(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	insertDelivery := func(okey string, seq int64, state string) string {
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
			did, eid, subID, okey, seq, state, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}

	// Key "aaa": a PRE-EXISTING cancelled row (sorts before "zzz"). Its key_state
	// is already drained (cursor past the cancelled seq1).
	insertDelivery("aaa", 1, "cancelled")
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, 'aaa', 1, 2, 0)`, subID); err != nil {
		t.Fatal(err)
	}

	// Key "zzz": head seq1 PENDING, cursor parked at 1.
	didZ := insertDelivery("zzz", 1, "pending")
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count, head_delivery_id)
		 VALUES ($1, 'zzz', 1, 1, 1, $2)`, subID, didZ); err != nil {
		t.Fatal(err)
	}

	// Soft-delete the subscription directly so its pending deliveries are orphans.
	// (We exercise the janitor path CancelOrphaned, not SoftDeleteSubscription.)
	if _, err := st.Pool.Exec(ctx,
		`UPDATE subscriptions SET deleted_at=now() WHERE id=$1`, subID); err != nil {
		t.Fatal(err)
	}

	// One janitor pass, batch=1: cancels the single pending orphan ("zzz" seq1).
	n, err := st.CancelOrphaned(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CancelOrphaned cancelled n=%d, want 1", n)
	}

	// "zzz" head must now be cancelled.
	var state string
	if err := st.Pool.QueryRow(ctx,
		`SELECT state FROM deliveries WHERE id=$1`, didZ).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Fatalf("zzz head state = %s, want cancelled", state)
	}

	// REGRESSION: "zzz" cursor must have advanced past the cancelled head to the
	// sentinel seq_counter+1 = 2. The old LIMIT-mismatch left it parked at 1.
	var cursor int64
	var blockedReason *string
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key='zzz'`,
		subID).Scan(&cursor, &blockedReason); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("zzz cursor after orphan-cancel = %d, want 2 (just-cancelled key not recomputed — BLOCKER-2)", cursor)
	}
	if blockedReason != nil {
		t.Fatalf("zzz blocked_reason = %v, want nil", blockedReason)
	}
}
