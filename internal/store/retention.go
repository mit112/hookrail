// internal/store/retention.go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// advisory lock keys (transaction-scoped; design §3, F9). Distinct per job so
// jobs don't serialize against each other unnecessarily.
const (
	lockTombstone = 0x484b_0001 // "HK"+1
	lockTrim      = 0x484b_0002
	lockIdem      = 0x484b_0003
	lockOrphan    = 0x484b_0004
)

func (s *Store) withAdvisoryTx(ctx context.Context, key int64, fn func(pgx.Tx) (int, error)) (int, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); err != nil {
		return 0, err
	}
	n, err := fn(tx)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// TombstoneEventPayloads empties payloads of OLD events with NO live delivery
// AND NO still-replayable dead-letter (design §3 job 1 / F1). age = replay
// expiry = RETENTION_EVENT_PAYLOAD_DAYS.
func (s *Store) TombstoneEventPayloads(ctx context.Context, age time.Duration, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockTombstone, func(tx pgx.Tx) (int, error) {
		ct, err := tx.Exec(ctx,
			`UPDATE events SET payload='{}', payload_size=0
			 WHERE id IN (
			   SELECT e.id FROM events e
			   WHERE e.payload_size > 0 AND e.created_at < now() - $1::interval
			     AND NOT EXISTS (
			       SELECT 1 FROM deliveries d
			       WHERE d.event_id = e.id AND (
			         d.state IN ('pending','retry_scheduled','in_flight')
			         OR (d.state = 'dead_lettered' AND EXISTS (
			               SELECT 1 FROM dead_letters dl
			               WHERE dl.delivery_id = d.id AND dl.replayed_at IS NULL
			                 AND dl.dead_at >= now() - $1::interval))))
			   LIMIT $2 FOR UPDATE SKIP LOCKED)`,
			age, batch)
		if err != nil {
			return 0, err
		}
		return int(ct.RowsAffected()), nil
	})
}

// TrimDeliveryAttempts deletes old attempt rows and, in the SAME tx, stamps
// attempts_truncated_at on each affected delivery so the marker is durable
// (design §3 job 2 / F12).
func (s *Store) TrimDeliveryAttempts(ctx context.Context, attemptAge time.Duration, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockTrim, func(tx pgx.Tx) (int, error) {
		rows, err := tx.Query(ctx,
			`WITH doomed AS (
			   SELECT id, delivery_id FROM delivery_attempts
			   WHERE completed_at < now() - $1::interval
			   LIMIT $2 FOR UPDATE SKIP LOCKED),
			 del AS (DELETE FROM delivery_attempts WHERE id IN (SELECT id FROM doomed) RETURNING delivery_id),
			 mark AS (UPDATE deliveries SET attempts_truncated_at = now()
			          WHERE id IN (SELECT DISTINCT delivery_id FROM del))
			 SELECT count(*) FROM del`,
			attemptAge, batch)
		if err != nil {
			return 0, err
		}
		defer rows.Close()
		var n int
		if rows.Next() {
			if err := rows.Scan(&n); err != nil {
				return 0, err
			}
		}
		return n, rows.Err()
	})
}

// PurgeIdempotency deletes expired idempotency keys, BOUNDED by batch (design
// §3 job 3 — every pass is bounded + SKIP LOCKED, no unbounded DELETE).
func (s *Store) PurgeIdempotency(ctx context.Context, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockIdem, func(tx pgx.Tx) (int, error) {
		ct, err := tx.Exec(ctx,
			`DELETE FROM idempotency_keys
			 WHERE (producer_key_id, idem_key) IN (
			   SELECT producer_key_id, idem_key FROM idempotency_keys
			   WHERE expires_at < now()
			   LIMIT $1 FOR UPDATE SKIP LOCKED)`, batch)
		if err != nil {
			return 0, err
		}
		return int(ct.RowsAffected()), nil
	})
}

// CancelOrphanedLocked runs the §4.2 reconciliation under the same transaction-
// scoped advisory lock pattern as the other jobs (design §3). cancelOrphanedTx
// is defined in admin.go.
func (s *Store) CancelOrphanedLocked(ctx context.Context, batch int) (int, error) {
	return s.withAdvisoryTx(ctx, lockOrphan, func(tx pgx.Tx) (int, error) {
		return cancelOrphanedTx(ctx, tx, batch)
	})
}
