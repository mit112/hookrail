//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

// TestSkipHeadBlockedAdvances verifies that SkipHead on a dead-lettered head:
// sets state='skipped', skip audit columns populated, cursor advances to
// next sequence, successor becomes claimable, and returns the new head id.
func TestSkipHeadBlockedAdvances(t *testing.T) {
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
		 blocked_reason, blocked_since, head_delivery_id)
		 VALUES ($1, $2, 2, 1, 2, 'dead_lettered', now(), 'will-be-set')
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create events and deliveries: seq 1 dead_lettered, seq 2 pending.
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

	// Verify initial: cursor=1, blocked
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

	// Skip the blocked head.
	nextHead, err := st.SkipHead(ctx, did1, "operator-1", "manual skip")
	if err != nil {
		t.Fatal(err)
	}
	if nextHead == nil {
		t.Fatal("SkipHead returned nil nextHead, want did2")
	}
	if *nextHead != did2 {
		t.Fatalf("SkipHead nextHead = %s, want %s", *nextHead, did2)
	}

	// Verify: cursor advanced to 2, blocked cleared
	var blockedSince *time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq, blocked_reason, blocked_since FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor, &blockedReason, &blockedSince); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("cursor after skip = %d, want 2", cursor)
	}
	if blockedReason != nil {
		t.Fatalf("blocked_reason after skip = %v, want nil", blockedReason)
	}
	if blockedSince != nil {
		t.Fatalf("blocked_since after skip = %v, want nil", blockedSince)
	}

	// Verify did1: state=skipped, skip audit columns set
	var state string
	var skippedBy, skipReason *string
	var skippedAt *time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT state, skipped_by, skip_reason, skipped_at FROM deliveries WHERE id=$1`,
		did1).Scan(&state, &skippedBy, &skipReason, &skippedAt); err != nil {
		t.Fatal(err)
	}
	if state != "skipped" {
		t.Fatalf("did1 state = %s, want skipped", state)
	}
	if skippedBy == nil || *skippedBy != "operator-1" {
		t.Fatalf("skipped_by = %v, want 'operator-1'", skippedBy)
	}
	if skipReason == nil || *skipReason != "manual skip" {
		t.Fatalf("skip_reason = %v, want 'manual skip'", skipReason)
	}
	if skippedAt == nil {
		t.Fatal("skipped_at is nil, want non-nil")
	}

	// Seq 2 is now claimable (successor becomes the head)
	claimed, _, err = st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("seq 2 not claimable after skip advanced cursor")
	}
}

// TestSkipHeadNonDeadLetteredRejected verifies that skipping a delivery
// that is not dead_lettered returns an error.
func TestSkipHeadNonDeadLetteredRejected(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": cursor=1, not blocked.
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 1, 1, 1)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create a pending delivery (not dead_lettered)
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
		did, eid, subID, "k1", int64(1), endpointID); err != nil {
		t.Fatal(err)
	}

	// Attempt to skip a pending delivery (not dead_lettered) → must fail
	_, err = st.SkipHead(ctx, did, "op", "reason")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("SkipHead non-dead-lettered: got %v, want ErrConflict", err)
	}
}

// TestSkipHeadNonHeadRejected verifies that skipping a delivery that is
// dead_lettered but NOT the current head returns an error.
func TestSkipHeadNonHeadRejected(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": cursor=1, blocked with dead_lettered at seq 1.
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count,
		 blocked_reason, blocked_since)
		 VALUES ($1, $2, 2, 1, 2, 'dead_lettered', now())
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1")
	if err != nil {
		t.Fatal(err)
	}

	// Create deliveries: seq 1 and seq 2, both dead_lettered.
	// SkipHead should only accept the one at cursor_seq (seq 1), not seq 2.
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
	_ = insertDelivery(1, "dead_lettered")
	did2 := insertDelivery(2, "dead_lettered")

	// Attempt to skip seq 2 while seq 1 is still head → must fail
	_, err = st.SkipHead(ctx, did2, "op", "reason")
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("SkipHead non-head: got %v, want ErrConflict", err)
	}
}

// TestSkipHeadIdempotent verifies that SkipHead is idempotent on an
// already-skipped delivery — returns the current head, no error.
func TestSkipHeadIdempotent(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	// Create deliveries first: seq 1 already skipped, seq 2 pending.
	eid1 := store.NewID()
	did1 := store.NewID()
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
		 VALUES ($1, $2, 'orders.created', $3, $4)`, eid1, keyID, []byte(`{}`), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id,
		 skipped_by, skip_reason, skipped_at)
		 VALUES ($1, $2, $3, $4, $5, 'skipped', now(), $6, 'prev-op', 'previous skip', now())`,
		did1, eid1, subID, "k1", int64(1), endpointID); err != nil {
		t.Fatal(err)
	}

	// Seq 2 is the current pending head
	eid2 := store.NewID()
	did2 := store.NewID()
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
		 VALUES ($1, $2, 'orders.created', $3, $4)`, eid2, keyID, []byte(`{}`), 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO deliveries (id, event_id, subscription_id, ordering_key, ordering_seq, state, next_attempt_at, endpoint_id)
		 VALUES ($1, $2, $3, $4, $5, 'pending', now(), $6)`,
		did2, eid2, subID, "k1", int64(2), endpointID); err != nil {
		t.Fatal(err)
	}

	// Seed ordered_key_state for key "k1": cursor=2 (past seq 1 which is already skipped),
	// seq_counter=2, backlog=1, not blocked. Use the actual did2 as head_delivery_id.
	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count, head_delivery_id)
		 VALUES ($1, $2, 2, 2, 1, $3)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "k1", did2)
	if err != nil {
		t.Fatal(err)
	}

	// Skip the already-skipped did1 → idempotent, returns current head (did2), no error
	nextHead, err := st.SkipHead(ctx, did1, "op2", "second attempt")
	if err != nil {
		t.Fatal(err)
	}
	if nextHead == nil || *nextHead != did2 {
		t.Fatalf("SkipHead idempotent nextHead = %v, want %s", nextHead, did2)
	}

	// Cursor should still be at 2 (unchanged by idempotent call)
	var cursor int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "k1").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != 2 {
		t.Fatalf("cursor after idempotent skip = %d, want 2", cursor)
	}
}
