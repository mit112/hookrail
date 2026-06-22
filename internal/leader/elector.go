package leader

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const SchedulerLeaderLockKey int64 = 0x484b0000

type Elector struct {
	dsn      string
	key      int64
	interval time.Duration
	isLeader func(bool)

	conn *pgx.Conn // standalone lock conn; nil when not leader (H1: NOT a pgxpool checkout)
	pid  uint32
}

func New(dsn string, key int64, interval time.Duration, isLeader func(bool)) *Elector {
	if isLeader == nil {
		isLeader = func(bool) {}
	}
	return &Elector{dsn: dsn, key: key, interval: interval, isLeader: isLeader}
}

func (e *Elector) BackendPID() uint32 {
	if e.conn == nil {
		return 0
	}
	return e.pid
}

// holdsLock probes pg_locks for the session advisory lock (ownership, not liveness).
func (e *Elector) holdsLock(ctx context.Context) (bool, error) {
	if e.conn == nil {
		return false, nil
	}
	classid := uint32(e.key >> 32) //nolint:gosec // advisory lock key split into 32-bit halves
	objid := uint32(e.key)         //nolint:gosec // always a valid positive advisory lock key
	var n int
	err := e.conn.QueryRow(ctx,
		`SELECT count(*) FROM pg_locks
		  WHERE locktype='advisory' AND pid=pg_backend_pid()
		    AND classid=$1 AND objid=$2 AND objsubid=1`,
		classid, objid).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// startupGuard confirms the advisory lock survives a follow-up statement on the
// same conn (H9 pooler guard). If a transaction-pooler rotated the backend
// between pg_try_advisory_lock and this probe, holdsLock returns false and we
// fail fast so the caller can log and retry.
func (e *Elector) startupGuard(ctx context.Context) error {
	held, err := e.holdsLock(ctx)
	if err != nil {
		return fmt.Errorf("startup guard: pooler or connection check failed: %w", err)
	}
	if !held {
		return fmt.Errorf("startup guard: advisory lock not observable on session; transaction pooler may have rotated the backend")
	}
	return nil
}

// Run blocks; while leader runs do each tick, stands down on lost ownership, re-elects every interval.
func (e *Elector) Run(ctx context.Context, tick time.Duration,
	onElected func(context.Context) error, do func(context.Context) error) error {
	defer e.release(context.Background())
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e.conn == nil { // standby: try to acquire
			got, err := e.tryAcquire(ctx)
			if err != nil || !got {
				if sleepCtx(ctx, e.interval) != nil {
					return ctx.Err()
				}
				continue
			}
			e.isLeader(true)
			_ = onElected(ctx) // startup sweep+reconcile, ONCE on election
			// Wait a full tick before the first Cycle so onElected+do don't run back-to-back.
			if sleepCtx(ctx, tick) != nil {
				e.isLeader(false)
				return ctx.Err()
			}
			continue
		}
		// leader: verify ownership, then do one cycle, then wait a tick
		held, err := e.holdsLock(ctx)
		if err != nil || !held {
			e.isLeader(false)
			e.release(ctx)
			// Wait interval before re-acquiring to give other electors a fair
			// chance on backend kill / connection loss (prevents flapping).
			if sleepCtx(ctx, e.interval) != nil {
				return ctx.Err()
			}
			continue
		}
		_ = do(ctx)
		if sleepCtx(ctx, tick) != nil {
			e.isLeader(false)
			return ctx.Err()
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// tryAcquire dials a fresh standalone conn and attempts the session lock.
// On false/err it closes the conn so it never lingers.
func (e *Elector) tryAcquire(ctx context.Context) (bool, error) {
	conn, err := pgx.Connect(ctx, e.dsn)
	if err != nil {
		return false, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, e.key).Scan(&got); err != nil {
		_ = conn.Close(ctx)
		return false, err
	}
	if !got {
		_ = conn.Close(ctx)
		return false, nil
	}
	e.conn = conn
	e.pid = conn.PgConn().PID()

	if err := e.startupGuard(ctx); err != nil {
		_, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, e.key)
		_ = conn.Close(ctx)
		e.conn = nil
		e.pid = 0
		return false, err
	}

	return true, nil
}

func (e *Elector) release(ctx context.Context) {
	if e.conn == nil {
		return
	}
	_, _ = e.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, e.key)
	_ = e.conn.Close(ctx)
	e.conn = nil
	e.pid = 0
}

// test wrappers
func (e *Elector) TryAcquireForTest(ctx context.Context) (bool, error) { return e.tryAcquire(ctx) }
func (e *Elector) ReleaseForTest(ctx context.Context)                  { e.release(ctx) }
func (e *Elector) StartupGuardForTest(ctx context.Context) error       { return e.startupGuard(ctx) }
