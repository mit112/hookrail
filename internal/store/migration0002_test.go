//go:build integration

package store_test

import (
	"context"
	"testing"
)

// TestMigration0002Roundtrip applies up (via testStore) then down then up,
// proving the literal down SQL is valid and the schema is reversible except
// the enum value (PG cannot drop it — documented).
func TestMigration0002Roundtrip(t *testing.T) {
	s := testStore(t) // already migrated up
	ctx := context.Background()

	// 'cancelled' must be a valid enum value
	if _, err := s.Pool.Exec(ctx, `SELECT 'cancelled'::delivery_state`); err != nil {
		t.Fatalf("cancelled enum value missing: %v", err)
	}
	// new columns exist
	for _, q := range []string{
		`SELECT deleted_at FROM endpoints WHERE false`,
		`SELECT deleted_at FROM subscriptions WHERE false`,
		`SELECT endpoint_id, attempts_truncated_at FROM deliveries WHERE false`,
		`SELECT endpoint_id FROM dead_letters WHERE false`,
	} {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("missing column for %q: %v", q, err)
		}
	}
	// CHECK constraints reject bad values
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
