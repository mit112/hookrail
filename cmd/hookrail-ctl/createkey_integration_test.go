//go:build integration

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/store"
)

// Self-provisions a migrated Postgres via testcontainers (like the store itests),
// then runs the ctl subprocess against it. The CI integration job provides only
// Docker (no DATABASE_URL/REDIS_ADDR), so the test must supply the DB itself.
func TestCreateProducerKeyEmitsKey(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	ctx := context.Background()
	pgc, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("hookrail"),
		tcpostgres.WithUsername("hookrail"),
		tcpostgres.WithPassword("hookrail"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgc.Terminate(ctx) })
	dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}
	// Apply the schema so the producer_keys INSERT has a table to write to.
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s.Close()

	cmd := exec.Command("go", "run", ".", "create-producer-key", "-name", "test")
	// REDIS_ADDR is validated by config.Load but never dialed by create-producer-key;
	// HOOKRAIL_MASTER_KEY must be 64 hex chars (unused by this path, validated at load).
	cmd.Env = append(os.Environ(),
		"DATABASE_URL="+dsn,
		"REDIS_ADDR=127.0.0.1:6379",
		"HOOKRAIL_MASTER_KEY="+strings.Repeat("0", 64),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "producer_key=hk_") {
		t.Fatalf("expected producer_key=hk_… in output, got: %s", out)
	}
}
