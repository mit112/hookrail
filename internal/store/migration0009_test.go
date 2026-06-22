//go:build integration

package store_test

import (
	"context"
	"testing"
)

func TestMigration0009AdminTokens(t *testing.T) {
	s := testStore(t) // already migrated up through 0009
	ctx := context.Background()

	// Table exists and a valid row inserts.
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO admin_tokens (id, token_hash, role, label) VALUES ($1, $2, $3, $4)`,
		"t1", []byte("hash-1-aaaaaaaaaaaaaaaaaaaaaaaa"), "operator", "ci"); err != nil {
		t.Fatalf("insert valid admin_token: %v", err)
	}

	// CHECK constraint rejects an unknown role.
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO admin_tokens (id, token_hash, role, label) VALUES ($1, $2, $3, $4)`,
		"t2", []byte("hash-2-bbbbbbbbbbbbbbbbbbbbbbbb"), "superuser", "bad"); err == nil {
		t.Fatal("expected CHECK violation for role='superuser'")
	}

	// Round-trip: down drops it, up recreates it.
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_tokens`).Scan(&n); err != nil {
		t.Fatalf("admin_tokens missing after round-trip: %v", err)
	}
}
