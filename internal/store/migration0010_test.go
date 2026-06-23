//go:build integration

package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func TestMigration0010ProducerKeyScopes(t *testing.T) {
	s := testStore(t) // already migrated up through 0010
	ctx := context.Background()

	// A key created after 0010 has no scope rows until one is inserted; the
	// composite PK rejects a duplicate (key, pattern).
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO producer_keys (id, key_hash, name) VALUES ($1, $2, $3)`,
		"k1", []byte("hash-k1-aaaaaaaaaaaaaaaaaaaaaaaa"), "k1"); err != nil {
		t.Fatalf("insert key: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO producer_key_scopes (producer_key_id, topic_pattern) VALUES ($1, $2)`,
		"k1", "*"); err != nil {
		t.Fatalf("insert scope: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO producer_key_scopes (producer_key_id, topic_pattern) VALUES ($1, $2)`,
		"k1", "*"); err == nil {
		t.Fatal("expected PK violation on duplicate (key, '*')")
	}

	// Round-trip integrity: full down drops it, up recreates it (empty).
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM producer_key_scopes`).Scan(&n); err != nil {
		t.Fatalf("producer_key_scopes missing after round-trip: %v", err)
	}
}

// TestMigration0010Backfill proves the '*' backfill is REAL, not vacuous: it
// drops just 0010 (back to v9), inserts a pre-R3 key with no scope rows, then
// re-applies 0010 and asserts the backfill granted that key a '*' scope. The
// full-rollback MigrateDown would wipe producer_keys, so this needs the
// version-stepping migrate instance below.
func TestMigration0010Backfill(t *testing.T) {
	s := testStore(t) // at v10
	ctx := context.Background()

	src, err := iofs.New(os.DirFS("migrations"), ".")
	if err != nil {
		t.Fatalf("iofs: %v", err)
	}
	dsn := s.Pool.Config().ConnString()
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5"+dsn[len("postgres"):])
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer func() { _, _ = m.Close() }()

	// Roll back just 0010 (producer_keys survives; producer_key_scopes gone).
	if err := m.Migrate(9); err != nil {
		t.Fatalf("migrate to 9: %v", err)
	}
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO producer_keys (id, key_hash, name) VALUES ($1, $2, $3)`,
		"legacy1", []byte("legacy-hash-aaaaaaaaaaaaaaaaaaaa"), "legacy"); err != nil {
		t.Fatalf("insert legacy key: %v", err)
	}

	// Re-apply 0010: creates the table AND backfills '*' for the legacy key.
	if err := m.Migrate(10); err != nil {
		t.Fatalf("migrate to 10: %v", err)
	}
	var pat string
	if err := s.Pool.QueryRow(ctx,
		`SELECT topic_pattern FROM producer_key_scopes WHERE producer_key_id=$1`,
		"legacy1").Scan(&pat); err != nil {
		t.Fatalf("backfill missing for legacy key: %v", err)
	}
	if pat != "*" {
		t.Fatalf("backfill pattern = %q, want '*'", pat)
	}
}
