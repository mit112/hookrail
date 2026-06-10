package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrStaleClaim: the caller's claim was superseded — its lease expired and
// another worker reclaimed the delivery. The stale worker must drop its
// result on the floor; the new owner's completion is authoritative.
var ErrStaleClaim = errors.New("store: stale claim, delivery owned by a newer claimant")

// ClaimedDelivery is everything a worker needs to attempt the POST.
type ClaimedDelivery struct {
	ID               string
	EventID          string
	SubscriptionID   string
	AttemptCount     int   // includes this claim (incremented atomically)
	ClaimVersion     int64 // monotonic fencing token — never reset (see CompleteAttempt)
	MaxAttempts      int
	Topic            string
	Payload          []byte
	EndpointID       string
	URL              string
	SecretCiphertext []byte
}

// ClaimDelivery is the §3.4 CAS: exactly one claimant wins; a live lease
// blocks takeover; an expired lease (crashed worker) is claimable. Both
// recovery layers (Redis PEL and PG sweeper) funnel through this, so
// overlapping recovery is collision-safe. 0 rows → caller acks and drops.
//
// Deviation from the §3.4 SQL as written: pending/retry_scheduled rows are
// claimable only once next_attempt_at is due. Without this, a duplicate
// Redis message (expected! at-least-once) would trigger an immediate retry,
// bypassing the backoff schedule that PG owns (§4).
// Every claim also bumps claim_version — the monotonic FENCING token that
// completions and releases must present (see CompleteAttempt). Unlike
// attempt_count it is never decremented (rate-limit release) and never reset
// (manual replay), so tokens can never be recycled (ABA).
func (s *Store) ClaimDelivery(ctx context.Context, id string, lease time.Duration) (bool, ClaimedDelivery, error) {
	var d ClaimedDelivery
	err := s.Pool.QueryRow(ctx, `
		WITH claimed AS (
			UPDATE deliveries SET
				state = 'in_flight',
				lease_until = now() + $2,
				attempt_count = attempt_count + 1,
				claim_version = claim_version + 1,
				updated_at = now()
			WHERE id = $1
			  AND ((state IN ('pending','retry_scheduled') AND next_attempt_at <= now())
			       OR (state = 'in_flight' AND lease_until < now()))
			RETURNING id, event_id, subscription_id, attempt_count, claim_version
		)
		SELECT c.id, c.event_id, c.subscription_id, c.attempt_count, c.claim_version,
		       sub.max_attempts, e.topic, e.payload, ep.id, ep.url, ep.secret_ciphertext
		FROM claimed c
		JOIN events e ON e.id = c.event_id
		JOIN subscriptions sub ON sub.id = c.subscription_id
		JOIN endpoints ep ON ep.id = sub.endpoint_id`,
		id, lease,
	).Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.AttemptCount, &d.ClaimVersion,
		&d.MaxAttempts, &d.Topic, &d.Payload, &d.EndpointID, &d.URL, &d.SecretCiphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ClaimedDelivery{}, nil // someone else owns it — ack & drop
	}
	if err != nil {
		return false, ClaimedDelivery{}, err
	}
	return true, d, nil
}

// ReleaseClaim un-claims a delivery WITHOUT consuming an attempt: local
// pre-HTTP gates (the rate limiter) must not burn consumer-facing attempt
// budget, so the decrement undoes the claim's increment. Fenced exactly like
// completion (state + attempt_count), so a stale release is impossible too.
func (s *Store) ReleaseClaim(ctx context.Context, id string, claimVersion int64, delay time.Duration) error {
	ct, err := s.Pool.Exec(ctx, `
		UPDATE deliveries SET state = 'retry_scheduled',
			attempt_count = attempt_count - 1,
			next_attempt_at = now() + $3,
			lease_until = NULL, updated_at = now()
		WHERE id = $1 AND state = 'in_flight' AND claim_version = $2`,
		id, claimVersion, delay)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleClaim
	}
	return nil
}
