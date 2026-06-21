package store

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// RecomputeCursorTx is the package-level variant of RecomputeCursor.
// It recomputes the ordered_key_state row for a given (subscription, ordering_key).
// The caller MUST hold the ordered_key_state row FOR UPDATE before calling this.
func RecomputeCursorTx(ctx context.Context, tx pgx.Tx, subID, key string) error {
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

// RecomputeCursor delegates to the package-level RecomputeCursorTx.
func (s *Store) RecomputeCursor(ctx context.Context, tx pgx.Tx, subID, key string) error {
	return RecomputeCursorTx(ctx, tx, subID, key)
}

// ApplyOrderedTerminalTx is the package-level terminal helper. Takes a FOR
// UPDATE lock on the ordered_key_state row first (LOCK ORDER), then recomputes
// the cursor, and returns the new head delivery id (nil if empty or blocked).
// No-op for ordering_key=="". Used by cancelOrphanedTx which has no *Store.
func ApplyOrderedTerminalTx(ctx context.Context, tx pgx.Tx, subID, key string) (*string, error) {
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
	if err := RecomputeCursorTx(ctx, tx, subID, key); err != nil {
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

// ApplyOrderedTerminal takes a FOR UPDATE lock on the ordered_key_state row
// FIRST (LOCK ORDER), then recomputes the cursor via RecomputeCursor, and
// returns the new head delivery id (nil if empty or blocked). No-op for
// ordering_key=="". The caller publishes the returned head to Redis AFTER
// commit (store never XADDs — BLOCKER-2).
func (s *Store) ApplyOrderedTerminal(ctx context.Context, tx pgx.Tx, subID, key string) (*string, error) {
	return ApplyOrderedTerminalTx(ctx, tx, subID, key)
}

// SkipHead moves a dead-lettered head delivery to 'skipped' state and
// advances the ordering key cursor. It is valid only when the delivery
// is (a) dead_lettered, and (b) the current head per ordered_key_state.
// Idempotent: if the delivery is already 'skipped', returns the current
// head without error. Returns the new head delivery id (nil if drained).
func (s *Store) SkipHead(ctx context.Context, deliveryID, operator, reason string) (*string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Locate the key: we need subscription_id + ordering_key to find the
	// ordered_key_state row. We deliberately DO NOT trust state/ordering_seq
	// read here — they are validated AFTER the key-state lock below (BLOCKER-1:
	// reading state before the lock is a TOCTOU; Task 14 Step 3 prescribes
	// locking ordered_key_state FIRST, then verifying head + dead_lettered).
	var subID, okey string
	err = tx.QueryRow(ctx,
		`SELECT subscription_id, COALESCE(ordering_key, '')
		 FROM deliveries WHERE id = $1`, deliveryID).Scan(&subID, &okey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("skip: delivery %s: %w", deliveryID, ErrNotFound)
		}
		return nil, fmt.Errorf("skip: lookup delivery: %w", err)
	}
	if okey == "" {
		return nil, fmt.Errorf("skip: delivery %s is not ordered: %w", deliveryID, ErrConflict)
	}

	// LOCK ORDER: lock ordered_key_state FOR UPDATE FIRST. Once held, no
	// concurrent CompleteAttempt / DeadLetterExhausted / ReplayDeadLetter /
	// SkipHead can transition this key's rows (they all take this lock first),
	// so the state/seq re-read below is authoritative.
	var cursorSeq int64
	if err := tx.QueryRow(ctx,
		`SELECT cursor_seq FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
		subID, okey).Scan(&cursorSeq); err != nil {
		return nil, fmt.Errorf("skip: lock key state: %w", err)
	}

	// Authoritative re-read of the delivery under the key-state lock.
	var orderingSeq int64
	var state string
	if err := tx.QueryRow(ctx,
		`SELECT ordering_seq, state FROM deliveries WHERE id = $1`,
		deliveryID).Scan(&orderingSeq, &state); err != nil {
		return nil, fmt.Errorf("skip: re-read delivery: %w", err)
	}

	// Idempotency: if already skipped, return current head, no error.
	if state == "skipped" {
		var headID *string
		if err := tx.QueryRow(ctx,
			`SELECT head_delivery_id FROM ordered_key_state
			 WHERE subscription_id=$1 AND ordering_key=$2`,
			subID, okey).Scan(&headID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return headID, nil
	}

	// Validate: must be the current head
	if orderingSeq != cursorSeq {
		return nil, fmt.Errorf("skip: delivery %s seq %d != cursor %d: %w",
			deliveryID, orderingSeq, cursorSeq, ErrConflict)
	}

	// Validate: must be dead_lettered
	if state != "dead_lettered" {
		return nil, fmt.Errorf("skip: delivery %s state=%s, want dead_lettered: %w",
			deliveryID, state, ErrConflict)
	}

	// Set state=skipped with audit columns. We hold the key-state lock and
	// re-read state above, so the guard cannot lose a race — but check
	// RowsAffected anyway (BLOCKER-1: never report success on a no-op update).
	ct, err := tx.Exec(ctx,
		`UPDATE deliveries SET
			state = 'skipped',
			skipped_by = $1,
			skip_reason = $2,
			skipped_at = now()
		 WHERE id = $3 AND state = 'dead_lettered'`,
		operator, reason, deliveryID)
	if err != nil {
		return nil, fmt.Errorf("skip: set skipped: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, fmt.Errorf("skip: delivery %s no longer dead_lettered: %w", deliveryID, ErrConflict)
	}

	// Recompute cursor (skipped ∈ T → cursor advances past this row)
	if err := s.RecomputeCursor(ctx, tx, subID, okey); err != nil {
		return nil, fmt.Errorf("skip: recompute cursor: %w", err)
	}

	// Read the new head
	var headID *string
	var blockedReason *string
	if err := tx.QueryRow(ctx,
		`SELECT head_delivery_id, blocked_reason FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, okey).Scan(&headID, &blockedReason); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// If new head is blocked, return nil (nothing to wake)
	if blockedReason != nil {
		return nil, nil
	}
	return headID, nil
}

// BlockedKeyRow is a blocked ordered key surfaced to the admin API.
type BlockedKeyRow struct {
	SubscriptionID        string     `json:"subscription_id"`
	OrderingKey           string     `json:"ordering_key"`
	HeadDeliveryID        *string    `json:"head_delivery_id"`
	BlockedSince          *time.Time `json:"blocked_since"`
	BacklogCount          int        `json:"backlog_count"`
	OldestSuccessorAgeSec *float64   `json:"oldest_successor_age_sec,omitempty"`
}

// BlockedKeyFilter controls BlockedKeys pagination.
type BlockedKeyFilter struct {
	AfterSubID string // keyset: subscription_id (empty = from start)
	AfterKey   string // keyset: ordering_key (empty = from start)
	Limit      int
}

// BlockedKeys returns keys whose head is dead_lettered (blocked_reason IS NOT NULL),
// with backlog and successor-age info. Paginated via keyset on (subscription_id, ordering_key).
func (s *Store) BlockedKeys(ctx context.Context, f BlockedKeyFilter) ([]BlockedKeyRow, error) {
	const terminalStates = `{succeeded,skipped,cancelled}`

	q := `SELECT oks.subscription_id, oks.ordering_key, oks.head_delivery_id,
		oks.blocked_since, oks.backlog_count,
		(SELECT EXTRACT(epoch FROM now() - min(d.created_at))
		 FROM deliveries d
		 WHERE d.subscription_id = oks.subscription_id
		   AND d.ordering_key    = oks.ordering_key
		   AND d.ordering_seq    > oks.cursor_seq
		   AND d.state          <> ALL($1::delivery_state[])) AS oldest_successor_age_sec
	FROM ordered_key_state oks
	WHERE oks.blocked_reason IS NOT NULL
	  AND (($2 = '' AND $3 = '')
	       OR (oks.subscription_id > $2)
	       OR (oks.subscription_id = $2 AND oks.ordering_key > $3))
	ORDER BY oks.subscription_id, oks.ordering_key
	LIMIT $4`

	rows, err := s.Pool.Query(ctx, q, terminalStates, f.AfterSubID, f.AfterKey, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BlockedKeyRow
	for rows.Next() {
		var r BlockedKeyRow
		if err := rows.Scan(&r.SubscriptionID, &r.OrderingKey, &r.HeadDeliveryID,
			&r.BlockedSince, &r.BacklogCount, &r.OldestSuccessorAgeSec); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
