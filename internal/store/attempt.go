package store

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
)

type AttemptResult struct {
	DeliveryID   string
	AttemptNo    int
	ClaimVersion int64 // the fencing token returned by ClaimDelivery
	Outcome      domain.Outcome
	HTTPStatus   int // 0 when no response
	ErrorClass   string
	LatencyMS    int
	RetryAfter   time.Duration // 0 when absent
	RequestedAt  time.Time
	CompletedAt  time.Time
}

// CompleteAttempt records the attempt and applies the §6 transition in ONE
// transaction — crash between the two is impossible by construction.
// FENCING: the transition only matches while the row is in_flight with the
// caller's claim_version. A worker whose lease expired (a new owner reclaimed
// and the version moved on) gets ErrStaleClaim and must drop its result —
// without this, a stale 404 could dead-letter a delivery that its new owner
// is busy delivering successfully. The fence is claim_version, NOT
// attempt_count: replay resets the count (§6), which would recycle tokens.
func (s *Store) CompleteAttempt(ctx context.Context, r AttemptResult, pol backoff.Policy, maxAttempts int) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// fenced transition FIRST: if the claim is stale, record nothing at all
	var ct pgconn.CommandTag
	switch {
	case r.Outcome == domain.OutcomeSuccess:
		ct, err = tx.Exec(ctx,
			`UPDATE deliveries SET state='succeeded', lease_until=NULL, updated_at=now()
			 WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
			r.DeliveryID, r.ClaimVersion)
	case r.Outcome == domain.OutcomePermanent || r.AttemptNo >= maxAttempts:
		ct, err = tx.Exec(ctx,
			`UPDATE deliveries SET state='dead_lettered', lease_until=NULL, updated_at=now()
			 WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
			r.DeliveryID, r.ClaimVersion)
	default: // retryable with budget left
		delay := pol.NextDelay(r.AttemptNo, r.RetryAfter, rand.New(rand.NewSource(time.Now().UnixNano()))) //nolint:gosec // jitter, not crypto
		ct, err = tx.Exec(ctx,
			`UPDATE deliveries SET state='retry_scheduled', lease_until=NULL,
			   next_attempt_at = now() + $3, updated_at=now()
			 WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
			r.DeliveryID, r.ClaimVersion, delay)
	}
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleClaim
	}

	var httpStatus *int
	if r.HTTPStatus != 0 {
		httpStatus = &r.HTTPStatus
	}
	status := map[domain.Outcome]string{
		domain.OutcomeSuccess:   "success",
		domain.OutcomeRetryable: "retryable_failure",
		domain.OutcomePermanent: "permanent_failure",
	}[r.Outcome]
	if _, err := tx.Exec(ctx,
		`INSERT INTO delivery_attempts
		   (delivery_id, attempt_no, claim_version, status, http_status, latency_ms, error_class, requested_at, completed_at)
		 VALUES ($1, $2, $3, $4::attempt_status, $5, $6, NULLIF($7,''), $8, $9)`,
		r.DeliveryID, r.AttemptNo, r.ClaimVersion, status, httpStatus, r.LatencyMS, r.ErrorClass,
		r.RequestedAt, r.CompletedAt); err != nil {
		return fmt.Errorf("insert attempt: %w", err)
	}

	if r.Outcome == domain.OutcomePermanent ||
		(r.Outcome == domain.OutcomeRetryable && r.AttemptNo >= maxAttempts) {
		// upsert: a replayed delivery that dead-letters AGAIN must show the
		// new error and timestamp, and shed its replayed_at marker
		if _, err := tx.Exec(ctx,
			`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
			 SELECT $1, $2, endpoint_id FROM deliveries WHERE id = $1
			 ON CONFLICT (delivery_id) DO UPDATE
			   SET final_error = EXCLUDED.final_error, dead_at = now(), replayed_at = NULL,
			       endpoint_id = EXCLUDED.endpoint_id`,
			r.DeliveryID, r.ErrorClass); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// DeadLetterExhausted dead-letters a delivery whose budget was consumed
// entirely by claims (hard-crash loops). NO consumer attempt happened under
// this claim, so NO delivery_attempts row is written, and the bookkeeping
// claim is un-counted — delivery_attempts never shows more than max_attempts
// rows (the published attempt contract). Fenced like every transition.
func (s *Store) DeadLetterExhausted(ctx context.Context, id string, claimVersion int64) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ct, err := tx.Exec(ctx,
		`UPDATE deliveries SET state='dead_lettered', attempt_count = attempt_count - 1,
		   lease_until=NULL, updated_at=now()
		 WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
		id, claimVersion)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrStaleClaim
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO dead_letters (delivery_id, final_error, endpoint_id)
		 SELECT $1, 'attempts_exhausted', endpoint_id FROM deliveries WHERE id = $1
		 ON CONFLICT (delivery_id) DO UPDATE
		   SET final_error = EXCLUDED.final_error, dead_at = now(), replayed_at = NULL,
		       endpoint_id = EXCLUDED.endpoint_id`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
