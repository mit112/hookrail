//go:build integration

package store_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/store"
)

var (
	once     sync.Once
	adminDSN string
	dbSeq    atomic.Int64
)

// testStore gives each test its OWN database inside one shared container:
// container startup is paid once, but tests stay isolated, order-independent,
// and safe to parallelize.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	once.Do(func() {
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
		adminDSN, err = pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("dsn: %v", err)
		}
	})
	name := fmt.Sprintf("hookrail_t%d", dbSeq.Add(1))
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	_ = admin.Close(ctx)
	s, err := store.Open(ctx, strings.Replace(adminDSN, "/hookrail?", "/"+name+"?", 1))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}
