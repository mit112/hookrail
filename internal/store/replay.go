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
// (2a) tombstone guard; (2) deleted-target guard; (3) fenced delivery reset (claim_version UNTOUCHED).
// For ordered deliveries, applies the terminal helper to recompute the cursor
// (blocked_reason cleared since the head is now pending). Returns the head
// delivery id for the admin handler to publish after commit.
func (s *Store) ReplayDeadLetter(ctx context.Context, deliveryID string, replayAge time.Duration) (ReplayOutcome, *string, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ReplayConflict, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// LOCK ORDER: look up ordering info and lock ordered_key_state FIRST.
	// If the delivery row is missing (ErrNoRows), fall through to the CAS —
	// classifyReplayMiss handles it correctly.
	var subID, orderingKey string
	err = tx.QueryRow(ctx,
		`SELECT subscription_id, COALESCE(ordering_key, '') FROM deliveries WHERE id = $1`,
		deliveryID).Scan(&subID, &orderingKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ReplayConflict, nil, err
	}
	deliveryExists := err == nil

	if deliveryExists && orderingKey != "" {
		var ok bool
		if err := tx.QueryRow(ctx,
			`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
			subID, orderingKey).Scan(&ok); err != nil {
			return ReplayConflict, nil, err
		}
	}

	// (1) linearization point: claim the replay iff un-replayed AND within $age.
	ct, err := tx.Exec(ctx,
		`UPDATE dead_letters SET replayed_at = now()
		 WHERE delivery_id = $1 AND replayed_at IS NULL AND dead_at >= now() - $2::interval`,
		deliveryID, replayAge)
	if err != nil {
		return ReplayConflict, nil, err
	}
	if ct.RowsAffected() == 0 {
		// Release THIS tx's pooled connection before classifyReplayMiss acquires
		// its own — otherwise every concurrent miss holds one conn while waiting
		// for a second, exhausting a small pool (CI: MaxConns=4) → deadlock.
		_ = tx.Rollback(ctx)
		out, err := s.classifyReplayMiss(ctx, deliveryID, replayAge) // read-only, own queries
		return out, nil, err
	}

	// (2a) tombstone guard: serialize against TombstoneEventPayloads on the events row.
	// TombstoneEventPayloads takes FOR UPDATE SKIP LOCKED on events and sets payload_size=0;
	// payload_size==0 can ONLY result from a tombstone (legit min payload is `{}` = 2 bytes,
	// payload is JSONB NOT NULL and required by the API). If tombstone won the row first, our
	// FOR UPDATE blocks until it commits, we read 0, and return ReplayGone (410) — never re-arm
	// a delivery with an emptied payload. If we win the row first, tombstone's SKIP LOCKED skips
	// this event and the payload stays intact.
	var psize int
	if err := tx.QueryRow(ctx,
		`SELECT e.payload_size FROM events e
		 JOIN deliveries d ON d.event_id = e.id
		 WHERE d.id = $1 FOR UPDATE OF e`, deliveryID).Scan(&psize); err != nil {
		return ReplayConflict, nil, err
	}
	if psize == 0 {
		return ReplayGone, nil, nil // payload reclaimed by retention → not replayable (rollback via defer)
	}

	// (3a) deleted-target guard: never resurrect a deleted destination.
	var depDeleted bool
	if err := tx.QueryRow(ctx,
		`SELECT (sub.deleted_at IS NOT NULL OR ep.deleted_at IS NOT NULL)
		 FROM deliveries d
		 JOIN subscriptions sub ON sub.id = d.subscription_id
		 JOIN endpoints ep ON ep.id = sub.endpoint_id
		 WHERE d.id = $1`, deliveryID).Scan(&depDeleted); err != nil {
		return ReplayConflict, nil, err
	}
	if depDeleted {
		return ReplayConflict, nil, nil // rollback via defer
	}

	// (3b) fenced reset: attempt_count→0, claim_version UNTOUCHED (design §4.1 step 4).
	ct2, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='pending', attempt_count=0, next_attempt_at=now(),
		   lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND state='dead_lettered'`, deliveryID)
	if err != nil {
		return ReplayConflict, nil, err
	}
	if ct2.RowsAffected() == 0 {
		return ReplayConflict, nil, nil // state changed under us → rollback
	}

	// Apply ordered terminal — recompute cursor; since head is now pending,
	// blocked_reason is cleared.  Returns the head id for the admin handler
	// to publish after commit.
	var nextHeadID *string
	if orderingKey != "" {
		nextHeadID, err = s.ApplyOrderedTerminal(ctx, tx, subID, orderingKey)
		if err != nil {
			return ReplayConflict, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ReplayConflict, nil, err
	}
	return ReplayOK, nextHeadID, nil
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
