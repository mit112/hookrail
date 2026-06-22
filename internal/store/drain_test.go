//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/store"
)

func TestReleaseClaimForDrainUnordered(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id := mkDelivery(t, s)

	// Claim the delivery.
	ok, d, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	// ReleaseClaimForDrain should move it to retry_scheduled.
	jitter := 2 * time.Second
	if err := s.ReleaseClaimForDrain(ctx, d.ID, d.ClaimVersion, jitter); err != nil {
		t.Fatalf("ReleaseClaimForDrain: %v", err)
	}

	// Verify state: retry_scheduled, attempt_count/claim_version unchanged,
	// lease_until NULL, next_attempt_at in the future.
	var state string
	var attemptCount int
	var claimVersion int64
	var leaseUntil *time.Time
	var nextAttemptAt time.Time
	if err := s.Pool.QueryRow(ctx,
		`SELECT state, attempt_count, claim_version, lease_until, next_attempt_at FROM deliveries WHERE id=$1`, id,
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
	if nextAttemptAt.Before(time.Now()) {
		t.Fatal("next_attempt_at must be in the future")
	}

	// Second call: 0 rows affected, no error (idempotent — already retry_scheduled).
	if err := s.ReleaseClaimForDrain(ctx, d.ID, d.ClaimVersion, jitter); err != nil {
		t.Fatalf("second ReleaseClaimForDrain should not error: %v", err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT state FROM deliveries WHERE id=$1`, id).Scan(&state); err != nil || state != "retry_scheduled" {
		t.Fatalf("state after second drain: %q (err=%v), want retry_scheduled", state, err)
	}

	// Stale claim_version: 0 rows, no error (another owner fenced it out).
	if err := s.ReleaseClaimForDrain(ctx, d.ID, d.ClaimVersion+99, jitter); err != nil {
		t.Fatalf("stale version should not error: %v", err)
	}

	// No-op on available / never-claimed delivery.
	keyID2 := seedPipeline(t, s, "drain.noop")
	res2, err := s.IngestEvent(ctx, store.IngestParams{
		ProducerKeyID: keyID2, Topic: "drain.noop", Payload: []byte(`{}`),
	})
	if err != nil || len(res2.DeliveryIDs) != 1 {
		t.Fatalf("ingest: err=%v deliveries=%d", err, len(res2.DeliveryIDs))
	}
	id2 := res2.DeliveryIDs[0]
	if err := s.ReleaseClaimForDrain(ctx, id2, 0, jitter); err != nil {
		t.Fatalf("no-op on available should not error: %v", err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT state FROM deliveries WHERE id=$1`, id2).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("state of unclaimed delivery: %q (err=%v), want pending", state, err)
	}
}
