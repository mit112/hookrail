//go:build integration

package store_test

import (
	"context"
	"testing"
)

func TestMigration0005CreatesDeadAtIndexConcurrently(t *testing.T) {
	s := testStore(t) // already migrated up through 0005
	ctx := context.Background()

	var exists bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='idx_dead_letters_dead_at')`,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("pg_indexes query: %v", err)
	}
	if !exists {
		t.Fatal("idx_dead_letters_dead_at not found — CONCURRENTLY migration did not create it")
	}
}
