//go:build integration

package store_test

import (
	"context"
	"testing"
)

func TestMigration0006Roundtrip(t *testing.T) {
	s := testStore(t) // already migrated up through 0006
	ctx := context.Background()

	// Verify new columns on subscriptions
	if _, err := s.Pool.Exec(ctx,
		`SELECT ordered FROM subscriptions WHERE false`); err != nil {
		t.Fatalf("subscriptions.ordered missing: %v", err)
	}

	// Verify new columns on deliveries
	for _, q := range []string{
		`SELECT ordering_key, ordering_seq FROM deliveries WHERE false`,
		`SELECT skipped_by, skip_reason, skipped_at FROM deliveries WHERE false`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("missing delivery column for %q: %v", q, err)
		}
	}

	// Verify ordered_key_state table exists
	if _, err := s.Pool.Exec(ctx,
		`SELECT subscription_id, ordering_key, seq_counter, cursor_seq,
		        head_delivery_id, blocked_reason, blocked_since,
		        backlog_count, updated_at
		   FROM ordered_key_state WHERE false`); err != nil {
		t.Fatalf("ordered_key_state table missing: %v", err)
	}

	// Verify indexes exist
	for _, idx := range []string{
		"deliveries_ordered_idx",
		"deliveries_ordered_blocking_idx",
	} {
		var exists bool
		err := s.Pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname=$1)`,
			idx).Scan(&exists)
		if err != nil || !exists {
			t.Fatalf("index %s: exists=%v, err=%v", idx, exists, err)
		}
	}

	// Round-trip: down then up
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
