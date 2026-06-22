package leader

import (
	"context"
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
