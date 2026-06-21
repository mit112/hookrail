package store

import "context"

// DueDeliveryIDs is the sweeper's scan (§3.3): everything due or stuck.
// Keyset-paginated on id (afterID = "" starts from the beginning): publishing
// does NOT change row state, so a plain LIMIT would re-read the same first
// batch on every call and starve everything behind it. The cursor lets one
// sweep walk the ENTIRE due set. Duplicate publication downstream is safe —
// workers claim idempotently (§3.4).
func (s *Store) DueDeliveryIDs(ctx context.Context, afterID string, limit int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM deliveries
		WHERE ((state IN ('pending','retry_scheduled') AND next_attempt_at <= now())
		   OR (state = 'in_flight' AND lease_until < now()))
		  AND id > $1
		  AND (ordering_key IS NULL OR EXISTS (
		      SELECT 1 FROM ordered_key_state oks
		      WHERE oks.subscription_id = deliveries.subscription_id
		        AND oks.ordering_key    = deliveries.ordering_key
		        AND oks.cursor_seq      = deliveries.ordering_seq))
		ORDER BY id
		LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
