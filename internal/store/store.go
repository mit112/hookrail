// Package store is the PostgreSQL source of truth (§3, §5): events,
// deliveries (the durable obligations + state machine), idempotency,
// attempts, DLQ. Redis never owns state — this package does.
package store

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	Pool *pgxpool.Pool
	dsn  string
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool, dsn: dsn}, nil
}

// OpenWithRetry opens the store, retrying Open (pool create + ping) on transient
// failures until success or maxWait elapses. A pod (re)started during a CNPG
// primary-failover window thus waits for the promoted primary instead of exiting.
// maxWait <= 0 is a single attempt (fail-fast, preserves Open semantics).
func OpenWithRetry(ctx context.Context, dsn string, maxWait time.Duration) (*Store, error) {
	return openWithRetry(ctx, maxWait, 250*time.Millisecond, func(c context.Context) (*Store, error) {
		return Open(c, dsn)
	})
}

func openWithRetry(ctx context.Context, maxWait, initialBackoff time.Duration, attempt func(context.Context) (*Store, error)) (*Store, error) {
	if initialBackoff <= 0 {
		initialBackoff = 250 * time.Millisecond // guard against a busy-spin
	}
	// maxWait <= 0: single attempt, fail-fast (preserves Open semantics).
	if maxWait <= 0 {
		s, err := attempt(ctx)
		if err != nil {
			return nil, fmt.Errorf("store: open did not succeed within %s: %w", maxWait, err)
		}
		return s, nil
	}
	// Hard overall bound: attempts run under a deadline context so a hanging
	// pool-create/Ping cannot block past maxWait (Codex M1 BLOCKER).
	dctx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	deadline := time.Now().Add(maxWait)
	const maxBackoff = 3 * time.Second
	backoff := initialBackoff
	var lastErr error
	for {
		s, err := attempt(dctx)
		if err == nil {
			return s, nil
		}
		lastErr = err
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("store: open did not succeed within %s: %w", maxWait, lastErr)
		}
		sleep := backoff
		if sleep > remaining {
			sleep = remaining // never sleep past the deadline
		}
		select {
		case <-dctx.Done():
			if ctx.Err() != nil { // parent cancelled (not our timeout)
				return nil, fmt.Errorf("store: open cancelled: %w", ctx.Err())
			}
			return nil, fmt.Errorf("store: open did not succeed within %s: %w", maxWait, lastErr)
		case <-time.After(sleep):
		}
		if backoff < maxBackoff {
			if backoff *= 2; backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func (s *Store) Close() { s.Pool.Close() }

// ErrNotFound: admin lookups/updates that match no live row (handler → 404).
var ErrNotFound = errors.New("store: not found")

// ErrConflict: state conflict (handler → 409).
var ErrConflict = errors.New("store: conflicting state")

func pgxTxRW() pgx.TxOptions { return pgx.TxOptions{} }

// Migrate applies all embedded migrations (golang-migrate, §5).
func (s *Store) Migrate() error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	// golang-migrate's pgx/v5 driver registers the "pgx5" URL scheme.
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5"+s.dsn[len("postgres"):])
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// MigrateDown rolls everything back (used only by the migration test).
func (s *Store) MigrateDown() error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5"+s.dsn[len("postgres"):])
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

// NewID returns a ULID (sortable, URL-safe — §5).
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}
