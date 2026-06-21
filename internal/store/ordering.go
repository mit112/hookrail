package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// AssignOrderingSeq atomically assigns the next ordering_seq for an
// ordering_key under a given subscription. The UPSERT locks the
// ordered_key_state row, serializing callers on that key. Returns the
// newly-assigned sequence number and current backlog count.
func (s *Store) AssignOrderingSeq(ctx context.Context, tx pgx.Tx, subID, key string) (int64, int, error) {
	var seq int64
	var backlog int
	err := tx.QueryRow(ctx, `
		INSERT INTO ordered_key_state (subscription_id, ordering_key, seq_counter, backlog_count, updated_at)
		VALUES ($1, $2, 1, 1, now())
		ON CONFLICT (subscription_id, ordering_key) DO UPDATE
		  SET seq_counter = ordered_key_state.seq_counter + 1,
		      backlog_count = ordered_key_state.backlog_count + 1,
		      updated_at = now()
		RETURNING seq_counter, backlog_count`, subID, key).Scan(&seq, &backlog)
	return seq, backlog, err
}

// RecomputeCursor recomputes the ordered_key_state row for a given
// (subscription, ordering_key). The caller MUST hold the ordered_key_state
// row FOR UPDATE before calling this. It sets cursor_seq from the
// Global-Constraints derivation, recomputes backlog_count, and sets
// head_delivery_id / blocked_reason / blocked_since from the head row.
func (s *Store) RecomputeCursor(ctx context.Context, tx pgx.Tx, subID, key string) error {
	// Derivation (Global Constraints verbatim):
	// cursor_seq = COALESCE(
	//   (SELECT min(ordering_seq) FROM deliveries
	//    WHERE subscription_id=$1 AND ordering_key=$2
	//    AND state <> ALL($3::delivery_state[])),
	//   seq_counter + 1
	// )
	// with $3 = {'succeeded','skipped','cancelled'}
	//
	// The ::delivery_state[] cast is REQUIRED — a bare pgx []string
	// will not compare against the delivery_state enum column.
	const terminalStates = `{succeeded,skipped,cancelled}`

	_, err := tx.Exec(ctx, `
		WITH recomputed AS (
			SELECT
				COALESCE(
					(SELECT min(ordering_seq) FROM deliveries
					 WHERE subscription_id = $1
					   AND ordering_key    = $2
					   AND state          <> ALL($3::delivery_state[])),
					oks.seq_counter + 1
				) AS new_cursor,
				COALESCE(
					(SELECT count(*) FROM deliveries
					 WHERE subscription_id = $1
					   AND ordering_key    = $2
					   AND state          <> ALL($3::delivery_state[])),
					0
				)::int AS new_backlog
			FROM ordered_key_state oks
			WHERE oks.subscription_id = $1
			  AND oks.ordering_key    = $2
		)
		UPDATE ordered_key_state oks SET
			cursor_seq    = r.new_cursor,
			backlog_count = r.new_backlog,
			head_delivery_id = (
				SELECT id FROM deliveries
				WHERE subscription_id = $1
				  AND ordering_key    = $2
				  AND ordering_seq    = r.new_cursor
			),
			blocked_reason = CASE
				WHEN EXISTS (
					SELECT 1 FROM deliveries
					WHERE subscription_id = $1
					  AND ordering_key    = $2
					  AND ordering_seq    = r.new_cursor
					  AND state           = 'dead_lettered'
				) THEN 'dead_lettered'
				ELSE NULL
			END,
			blocked_since = CASE
				WHEN EXISTS (
					SELECT 1 FROM deliveries
					WHERE subscription_id = $1
					  AND ordering_key    = $2
					  AND ordering_seq    = r.new_cursor
					  AND state           = 'dead_lettered'
				) THEN COALESCE(oks.blocked_since, now())
				ELSE NULL
			END,
			updated_at    = now()
		FROM recomputed r
		WHERE oks.subscription_id = $1
		  AND oks.ordering_key    = $2`,
		subID, key, terminalStates)
	return err
}

// ApplyOrderedTerminal takes a FOR UPDATE lock on the ordered_key_state row
// FIRST (LOCK ORDER), then recomputes the cursor via RecomputeCursor, and
// returns the new head delivery id (nil if empty or blocked). No-op for
// ordering_key=="". The caller publishes the returned head to Redis AFTER
// commit (store never XADDs — BLOCKER-2).
func (s *Store) ApplyOrderedTerminal(ctx context.Context, tx pgx.Tx, subID, key string) (*string, error) {
	if key == "" {
		return nil, nil
	}
	// LOCK ORDER: lock ordered_key_state row FOR UPDATE first
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT true FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
		subID, key).Scan(&ok); err != nil {
		return nil, err
	}
	if err := s.RecomputeCursor(ctx, tx, subID, key); err != nil {
		return nil, err
	}
	var headID *string
	var blockedReason *string
	if err := tx.QueryRow(ctx,
		`SELECT head_delivery_id, blocked_reason FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, key).Scan(&headID, &blockedReason); err != nil {
		return nil, err
	}
	// A blocked key (dead_lettered head) returns nil — nothing to wake.
	if blockedReason != nil {
		return nil, nil
	}
	return headID, nil
}
