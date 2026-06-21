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
