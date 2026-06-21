package store

import "context"

// reconcileOneKey self-heals one ordered key's cursor projection from the
// deliveries source-of-truth in its own tx, under the global LOCK ORDER
// (ordered_key_state FOR UPDATE first, then read deliveries). It returns the
// post-recompute head id, the head delivery's state (empty if drained), whether
// the key is blocked on a dead_lettered head, and the recomputed backlog.
func (s *Store) reconcileOneKey(ctx context.Context, subID, key string) (head *string, headState string, blocked bool, backlog int, err error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, "", false, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// LOCK ORDER: ordered_key_state row FOR UPDATE FIRST, then recompute.
	var dummy bool
	if err = tx.QueryRow(ctx,
		`SELECT true FROM ordered_key_state WHERE subscription_id=$1 AND ordering_key=$2 FOR UPDATE`,
		subID, key).Scan(&dummy); err != nil {
		return nil, "", false, 0, err
	}
	if err = RecomputeCursorTx(ctx, tx, subID, key); err != nil {
		return nil, "", false, 0, err
	}

	var blockedReason *string
	if err = tx.QueryRow(ctx,
		`SELECT head_delivery_id, blocked_reason, backlog_count FROM ordered_key_state
		 WHERE subscription_id=$1 AND ordering_key=$2`,
		subID, key).Scan(&head, &blockedReason, &backlog); err != nil {
		return nil, "", false, 0, err
	}
	blocked = blockedReason != nil
	if head != nil {
		if err = tx.QueryRow(ctx,
			`SELECT state::text FROM deliveries WHERE id=$1`, *head).Scan(&headState); err != nil {
			return nil, "", false, 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", false, 0, err
	}
	return head, headState, blocked, backlog, nil
}

// ReconcileKey recomputes a single ordered key's cursor from deliveries (FOR
// UPDATE → recompute) and returns the deliverable head id (nil if drained or
// blocked). The caller republishes the head to Redis AFTER this returns — the
// store never XADDs.
func (s *Store) ReconcileKey(ctx context.Context, subID, key string) (*string, error) {
	head, _, blocked, _, err := s.reconcileOneKey(ctx, subID, key)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, nil
	}
	return head, nil
}

// ReconcileOrderedKeys walks every ordered key in keyset batches, self-heals its
// cursor projection, and republishes a deliverable head that may have been lost
// from Redis (the FLUSHALL backstop — in_flight heads are being worked, so they
// are skipped). It returns the count of blocked keys and the total backlog across
// all keys; the caller sets the Prometheus gauges (store stays obs-free).
// publish may be nil (stats-only pass).
func (s *Store) ReconcileOrderedKeys(ctx context.Context, batch int, publish func(context.Context, string) error) (blockedKeys int, totalBacklog int64, err error) {
	if batch <= 0 {
		batch = 1000
	}
	type keyRef struct{ sub, key string }
	afterSub, afterKey := "", ""
	for {
		var keys []keyRef
		rows, qerr := s.Pool.Query(ctx, `
			SELECT subscription_id, ordering_key FROM ordered_key_state
			WHERE (subscription_id, ordering_key) > ($1, $2)
			ORDER BY subscription_id, ordering_key
			LIMIT $3`, afterSub, afterKey, batch)
		if qerr != nil {
			return blockedKeys, totalBacklog, qerr
		}
		for rows.Next() {
			var k keyRef
			if serr := rows.Scan(&k.sub, &k.key); serr != nil {
				rows.Close()
				return blockedKeys, totalBacklog, serr
			}
			keys = append(keys, k)
		}
		rows.Close()
		if rerr := rows.Err(); rerr != nil {
			return blockedKeys, totalBacklog, rerr
		}
		if len(keys) == 0 {
			break
		}
		for _, k := range keys {
			head, headState, blocked, backlog, rcerr := s.reconcileOneKey(ctx, k.sub, k.key)
			if rcerr != nil {
				return blockedKeys, totalBacklog, rcerr
			}
			if blocked {
				blockedKeys++
			}
			totalBacklog += int64(backlog)
			if !blocked && head != nil && publish != nil &&
				(headState == "pending" || headState == "retry_scheduled") {
				_ = publish(ctx, *head) // best-effort; due-sweeper + next tick retry
			}
		}
		last := keys[len(keys)-1]
		afterSub, afterKey = last.sub, last.key
		if len(keys) < batch {
			break
		}
	}
	return blockedKeys, totalBacklog, nil
}
