package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReleaseClaimForDrain fenced-releases an in-flight delivery so another replica
// can pick it up during graceful drain.  Unlike the rate-limit ReleaseClaim:
//   - It does NOT decrement attempt_count (no attempt was consumed).
//   - It does NOT touch claim_version (the fencing token stays monotonic).
//   - It sets state=retry_scheduled with a near-future next_attempt_at,
//     NEVER in_flight+NULL lease (that would strand the row forever).
//
// For ordered deliveries (ordering_key IS NOT NULL), the release runs in a
// transaction that locks ordered_key_state FOR UPDATE first (global LOCK ORDER,
// matching CompleteAttempt), then recomputes the cursor via RecomputeCursorTx.
//
// 0 rows affected (state changed, stale claim_version, or row doesn't exist)
// is NOT an error — another owner already took it.
func (s *Store) ReleaseClaimForDrain(ctx context.Context, id string, claimVersion int64, jitter time.Duration) error {
	// Check if this is an ordered delivery.
	var orderingKey *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT ordering_key FROM deliveries WHERE id = $1`, id,
	).Scan(&orderingKey); err != nil {
		if err == pgx.ErrNoRows {
			return nil // delivery doesn't exist — nothing to release
		}
		return err
	}

	if orderingKey == nil {
		// Unordered path: single UPDATE, no transaction needed.
		_, err := s.Pool.Exec(ctx,
			`UPDATE deliveries
				SET state='retry_scheduled',
				    next_attempt_at = now() + $3,
				    lease_until = NULL,
				    updated_at = now()
			  WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
			id, claimVersion, jitter)
		return err
	}

	// Ordered path: transaction with LOCK ORDER.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Re-read subID + orderingKey under transaction.
	var subID, okey string
	if err := tx.QueryRow(ctx,
		`SELECT subscription_id, COALESCE(ordering_key, '') FROM deliveries WHERE id = $1`,
		id).Scan(&subID, &okey); err != nil {
		return err
	}

	// LOCK ORDER: lock ordered_key_state BEFORE touching deliveries.
	if okey != "" {
		var ok bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
			subID, okey).Scan(&ok); err != nil {
			return err
		}
	}

	// Fenced UPDATE.
	ct, err := tx.Exec(ctx,
		`UPDATE deliveries
			SET state='retry_scheduled',
			    next_attempt_at = now() + $3,
			    lease_until = NULL,
			    updated_at = now()
		  WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
		id, claimVersion, jitter)
	if err != nil {
		return err
	}

	// If the UPDATE affected 0 rows, still recompute cursor (the row may have
	// been completed/dead-lettered by another owner — cursor must reflect it).
	if okey != "" {
		if err := RecomputeCursorTx(ctx, tx, subID, okey); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	_ = ct // 0 rows is fine — another owner took it
	return nil
}
