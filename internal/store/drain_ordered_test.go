//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestReleaseClaimForDrainOrdered(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()

	keyID, subID := seedOrderedPipeline(t, st, "orders.*")

	var endpointID string
	if err := st.Pool.QueryRow(ctx,
		`SELECT endpoint_id FROM subscriptions WHERE id=$1`, subID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	_, err := st.Pool.Exec(ctx,
		`INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, cursor_seq, backlog_count)
		 VALUES ($1, $2, 2, 1, 2)
		 ON CONFLICT (subscription_id, ordering_key) DO NOTHING`,
		subID, "drain-k1")
	if err != nil {
		t.Fatal(err)
	}

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
			did, eid, subID, "drain-k1", seq, endpointID); err != nil {
			t.Fatal(err)
		}
		return did
	}
	did1 := insertDelivery(1)
	did2 := insertDelivery(2)

	claimed, d, err := st.ClaimDelivery(ctx, did1, 30*time.Second)
	if err != nil || !claimed {
		t.Fatalf("claim head: ok=%v err=%v", claimed, err)
	}

	jitter := 2 * time.Second
	// Use 0 jitter for the initial drain so the delivery is immediately reclaimable.
	if err := st.ReleaseClaimForDrain(ctx, d.ID, d.ClaimVersion, 0); err != nil {
		t.Fatalf("ReleaseClaimForDrain ordered: %v", err)
	}

	var state string
	var attemptCount int
	var claimVersion int64
	var leaseUntil *time.Time
	var nextAttemptAt time.Time
	if err := st.Pool.QueryRow(ctx,
		`SELECT state, attempt_count, claim_version, lease_until, next_attempt_at FROM deliveries WHERE id=$1`, did1,
	).Scan(&state, &attemptCount, &claimVersion, &leaseUntil, &nextAttemptAt); err != nil {
		t.Fatal(err)
	}
	if state != "retry_scheduled" {
		t.Fatalf("state=%q, want retry_scheduled", state)
	}
	if attemptCount != d.AttemptCount {
		t.Fatalf("attempt_count=%d, want %d (unchanged)", attemptCount, d.AttemptCount)
	}
	if claimVersion != d.ClaimVersion {
		t.Fatalf("claim_version=%d, want %d (unchanged)", claimVersion, d.ClaimVersion)
	}
	if leaseUntil != nil {
		t.Fatal("lease_until must be NULL after drain release")
	}
	if nextAttemptAt.IsZero() {
		t.Fatal("next_attempt_at must be set after drain release")
	}

	var cursorSeq int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, "drain-k1").Scan(&cursorSeq); err != nil {
		t.Fatal(err)
	}
	if cursorSeq != 1 {
		t.Fatalf("cursor_seq=%d, want 1 (unchanged)", cursorSeq)
	}

	claimed2, d2, err := st.ClaimDelivery(ctx, did1, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed2 {
		t.Fatal("released ordered head must be reclaimable")
	}
	if d2.AttemptCount != d.AttemptCount+1 {
		t.Fatalf("reclaim attempt_count=%d, want %d", d2.AttemptCount, d.AttemptCount+1)
	}

	claimed3, _, err := st.ClaimDelivery(ctx, did2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if claimed3 {
		t.Fatal("successor (seq 2) must be blocked while seq 1 is the head")
	}

	if err := st.ReleaseClaimForDrain(ctx, d2.ID, d2.ClaimVersion, jitter); err != nil {
		t.Fatalf("second ReleaseClaimForDrain on ordered: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT state FROM deliveries WHERE id=$1`, did1).Scan(&state); err != nil || state != "retry_scheduled" {
		t.Fatalf("state after second drain: %q (err=%v), want retry_scheduled", state, err)
	}

	if err := st.ReleaseClaimForDrain(ctx, d2.ID, d2.ClaimVersion+99, jitter); err != nil {
		t.Fatalf("stale version on ordered should not error: %v", err)
	}
}
