//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
)

func TestMigration0007SkippedEnum(t *testing.T) {
	s := testStore(t) // already migrated up through 0007
	ctx := context.Background()

	// Verify 'skipped' is present in the delivery_state enum
	var enumRange string
	err := s.Pool.QueryRow(ctx,
		`SELECT enum_range(NULL::delivery_state)::text`,
	).Scan(&enumRange)
	if err != nil {
		t.Fatalf("enum_range query: %v", err)
	}
	if !strings.Contains(enumRange, "skipped") {
		t.Fatalf("delivery_state enum does not contain 'skipped'; got: %s", enumRange)
	}

	// Round-trip: down then up, confirm skipped still present
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}

	// Verify skipped still present after round-trip
	err = s.Pool.QueryRow(ctx,
		`SELECT enum_range(NULL::delivery_state)::text`,
	).Scan(&enumRange)
	if err != nil {
		t.Fatalf("enum_range after round-trip: %v", err)
	}
	if !strings.Contains(enumRange, "skipped") {
		t.Fatalf("delivery_state enum missing 'skipped' after round-trip; got: %s", enumRange)
	}
}
