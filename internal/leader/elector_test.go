//go:build integration

package leader_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mit112/hookrail/internal/leader"
)

var (
	pgOnce sync.Once
	pgDSN  string
)

func startPG(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	pgOnce.Do(func() {
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
		dsn, err := pgc.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("dsn: %v", err)
		}
		pgDSN = dsn
	})
	return pgDSN
}

func TestElectorSingleAcquire(t *testing.T) {
	dsn := startPG(t)
	a := leader.New(dsn, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(bool) {})
	b := leader.New(dsn, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(bool) {})
	ctx := context.Background()

	okA, err := a.TryAcquireForTest(ctx)
	if err != nil || !okA {
		t.Fatalf("a should acquire: ok=%v err=%v", okA, err)
	}
	if a.BackendPID() == 0 {
		t.Fatal("leader must expose a backend pid")
	}

	okB, err := b.TryAcquireForTest(ctx)
	if err != nil {
		t.Fatalf("b probe err: %v", err)
	}
	if okB {
		t.Fatal("b must NOT acquire while a holds the lock")
	}

	a.ReleaseForTest(ctx)
	okB2, err := b.TryAcquireForTest(ctx)
	if err != nil || !okB2 {
		t.Fatalf("b should acquire after a releases: ok=%v err=%v", okB2, err)
	}
	b.ReleaseForTest(ctx)
}
