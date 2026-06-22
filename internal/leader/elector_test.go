//go:build integration

package leader_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func killBackend(t *testing.T, dsn string, pid uint32) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("killBackend connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
	if err != nil {
		t.Fatalf("pg_terminate_backend(%d): %v", pid, err)
	}
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

func TestElectorFailoverOnBackendKill(t *testing.T) {
	dsn := startPG(t)
	var aLeader, bLeader atomic.Bool
	var aDo, bDo atomic.Int64
	a := leader.New(dsn, leader.SchedulerLeaderLockKey, 100*time.Millisecond, func(v bool) { aLeader.Store(v) })
	b := leader.New(dsn, leader.SchedulerLeaderLockKey, 100*time.Millisecond, func(v bool) { bLeader.Store(v) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	noop := func(context.Context) error { return nil }
	aCycle := func(context.Context) error { aDo.Add(1); return nil }
	bCycle := func(context.Context) error { bDo.Add(1); return nil }
	go func() { _ = a.Run(ctx, 50*time.Millisecond, noop, aCycle) }()
	go func() { _ = b.Run(ctx, 50*time.Millisecond, noop, bCycle) }()

	// exactly one becomes leader
	waitFor(t, 2*time.Second, func() bool { return aLeader.Load() != bLeader.Load() })

	// CAPTURE which one leads BEFORE the kill
	var leaderEl, standbyEl *leader.Elector
	var leaderFlag, standbyFlag *atomic.Bool
	var standbyDo *atomic.Int64
	if aLeader.Load() {
		leaderEl, standbyEl = a, b
		leaderFlag, standbyFlag, standbyDo = &aLeader, &bLeader, &bDo
	} else {
		leaderEl, standbyEl = b, a
		leaderFlag, standbyFlag, standbyDo = &bLeader, &aLeader, &aDo
	}
	deadPID := leaderEl.BackendPID()
	if deadPID == 0 {
		t.Fatal("leader must expose a backend pid")
	}
	doBefore := standbyDo.Load()

	killBackend(t, dsn, deadPID)

	// the killed leader must drop, the standby must PROMOTE on a new/different backend,
	// and the promoted elector's Cycle must start running (proves it actually took over).
	waitFor(t, 3*time.Second, func() bool { return !leaderFlag.Load() && standbyFlag.Load() })
	waitFor(t, 3*time.Second, func() bool { return standbyEl.BackendPID() != 0 && standbyEl.BackendPID() != deadPID })
	waitFor(t, 3*time.Second, func() bool { return standbyDo.Load() > doBefore })
}

func TestElectorOnElectedOnce(t *testing.T) {
	dsn := startPG(t)
	var electedCount, cycleCount atomic.Int64
	a := leader.New(dsn, leader.SchedulerLeaderLockKey, 100*time.Millisecond,
		func(v bool) {
			if v {
				electedCount.Add(1)
			}
		})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	onElected := func(context.Context) error { return nil }
	do := func(context.Context) error { cycleCount.Add(1); return nil }
	go func() { _ = a.Run(ctx, 50*time.Millisecond, onElected, do) }()

	// Wait for several cycles to accumulate, then verify onElected fired exactly once
	waitFor(t, 2*time.Second, func() bool { return cycleCount.Load() >= 3 })
	if n := electedCount.Load(); n != 1 {
		t.Fatalf("onElected should fire exactly once per acquisition, got %d", n)
	}
	cancel()
	time.Sleep(100 * time.Millisecond) // let goroutine exit and release lock
}

func TestElectorGracefulRelease(t *testing.T) {
	dsn := startPG(t)
	var aLeader, bLeader atomic.Bool
	a := leader.New(dsn, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(v bool) { aLeader.Store(v) })
	b := leader.New(dsn, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(v bool) { bLeader.Store(v) })
	aCtx, aCancel := context.WithCancel(context.Background())
	bCtx, bCancel := context.WithCancel(context.Background())
	noop := func(context.Context) error { return nil }
	go func() { _ = a.Run(aCtx, 50*time.Millisecond, noop, noop) }()
	go func() { _ = b.Run(bCtx, 50*time.Millisecond, noop, noop) }()

	// Exactly one becomes leader
	waitFor(t, 2*time.Second, func() bool { return aLeader.Load() != bLeader.Load() })

	// Cancel the leader's context => graceful release
	var standbyFlag *atomic.Bool
	var standbyCancel context.CancelFunc
	if aLeader.Load() {
		aCancel()
		standbyFlag = &bLeader
		standbyCancel = bCancel
	} else {
		bCancel()
		standbyFlag = &aLeader
		standbyCancel = aCancel
	}

	// The standby (still-running ctx) must become leader promptly
	waitFor(t, 3*time.Second, func() bool { return standbyFlag.Load() })

	// Clean up the standby's goroutine
	standbyCancel()
	time.Sleep(100 * time.Millisecond) // let goroutines exit
}

func TestElectorStartupGuard(t *testing.T) {
	dsn := startPG(t)
	a := leader.New(dsn, leader.SchedulerLeaderLockKey, 200*time.Millisecond, func(bool) {})
	ctx := context.Background()

	// Acquire the lock, then startupGuard must pass (lock is observable on the session).
	ok, err := a.TryAcquireForTest(ctx)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	if err := a.StartupGuardForTest(ctx); err != nil {
		t.Fatalf("startupGuard must pass when lock is held: %v", err)
	}
	a.ReleaseForTest(ctx)

	// When no lock is held, startupGuard must fail (ownership not observable —
	// simulates what happens when a transaction pooler rotates the backend).
	if err := a.StartupGuardForTest(ctx); err == nil {
		t.Fatal("startupGuard must fail when lock is not held on the session")
	}
}
