package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type ReplayOutcome int

const (
	ReplayOK       ReplayOutcome = iota // 200
	ReplayNotFound                      // 404
	ReplayConflict                      // 409
	ReplayGone                          // 410
)

// ReplayDeadLetter atomically re-arms a dead-lettered delivery (design §4.1).
// One tx: (1) atomic replay-claim CAS — exactly one concurrent winner;
// (2) deleted-target guard; (3) fenced delivery reset (claim_version UNTOUCHED).
// On a CAS miss it classifies 404/409/410 from a read-back. Best-effort XADD
// is the caller's job after commit.
func (s *Store) ReplayDeadLetter(ctx context.Context, deliveryID string, replayAge time.Duration) (ReplayOutcome, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ReplayConflict, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// (1) linearization point: claim the replay iff un-replayed AND within $age.
	ct, err := tx.Exec(ctx,
		`UPDATE dead_letters SET replayed_at = now()
		 WHERE delivery_id = $1 AND replayed_at IS NULL AND dead_at >= now() - $2::interval`,
		deliveryID, replayAge)
	if err != nil {
		return ReplayConflict, err
	}
	if ct.RowsAffected() == 0 {
		return s.classifyReplayMiss(ctx, deliveryID, replayAge) // read-only, own queries
	}

	// (3a) deleted-target guard: never resurrect a deleted destination.
	var depDeleted bool
	if err := tx.QueryRow(ctx,
		`SELECT (sub.deleted_at IS NOT NULL OR ep.deleted_at IS NOT NULL)
		 FROM deliveries d
		 JOIN subscriptions sub ON sub.id = d.subscription_id
		 JOIN endpoints ep ON ep.id = sub.endpoint_id
		 WHERE d.id = $1`, deliveryID).Scan(&depDeleted); err != nil {
		return ReplayConflict, err
	}
	if depDeleted {
		return ReplayConflict, nil // rollback via defer
	}

	// (3b) fenced reset: attempt_count→0, claim_version UNTOUCHED (design §4.1 step 4).
	ct2, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='pending', attempt_count=0, next_attempt_at=now(),
		   lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND state='dead_lettered'`, deliveryID)
	if err != nil {
		return ReplayConflict, err
	}
	if ct2.RowsAffected() == 0 {
		return ReplayConflict, nil // state changed under us → rollback
	}
	if err := tx.Commit(ctx); err != nil {
		return ReplayConflict, err
	}
	return ReplayOK, nil
}

func (s *Store) classifyReplayMiss(ctx context.Context, deliveryID string, replayAge time.Duration) (ReplayOutcome, error) {
	var alreadyReplayed, expired bool
	err := s.Pool.QueryRow(ctx,
		`SELECT replayed_at IS NOT NULL, dead_at < now() - $2::interval
		 FROM dead_letters WHERE delivery_id=$1`, deliveryID, replayAge).
		Scan(&alreadyReplayed, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deliveries WHERE id=$1)`, deliveryID).Scan(&exists); err != nil {
			return ReplayConflict, err
		}
		if !exists {
			return ReplayNotFound, nil // unknown delivery
		}
		return ReplayConflict, nil // live delivery, not dead-lettered (design §2.2)
	}
	if err != nil {
		return ReplayConflict, err
	}
	if expired && !alreadyReplayed {
		return ReplayGone, nil
	}
	return ReplayConflict, nil // already replayed, or any other non-claimable state
}
