//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/backoff"
	"github.com/mit112/hookrail/internal/domain"
	"github.com/mit112/hookrail/internal/store"
)

// claimFresh seeds and claims one delivery, returning the claim.
func claimFresh(t *testing.T, s *store.Store) store.ClaimedDelivery {
	t.Helper()
	id := mkDelivery(t, s)
	ok, d, err := s.ClaimDelivery(context.Background(), id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	return d
}

func deliveryState(t *testing.T, s *store.Store, id string) (state string, nextAt time.Time) {
	t.Helper()
	err := s.Pool.QueryRow(context.Background(),
		`SELECT state, next_attempt_at FROM deliveries WHERE id = $1`, id).Scan(&state, &nextAt)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func result(d store.ClaimedDelivery, outcome domain.Outcome, status int, errClass string) store.AttemptResult {
	now := time.Now()
	return store.AttemptResult{
		DeliveryID: d.ID, AttemptNo: d.AttemptCount, ClaimVersion: d.ClaimVersion, Outcome: outcome,
		HTTPStatus: status, ErrorClass: errClass, LatencyMS: 12,
		RequestedAt: now.Add(-12 * time.Millisecond), CompletedAt: now,
	}
}

func TestAttemptSuccess(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)
	if err := s.CompleteAttempt(context.Background(), result(d, domain.OutcomeSuccess, 200, ""), backoff.Default(), d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	st, _ := deliveryState(t, s, d.ID)
	if st != "succeeded" {
		t.Fatalf("state = %s, want succeeded", st)
	}
	var n int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id = $1`, d.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("attempts = %d (err %v), want 1", n, err)
	}
}

func TestAttemptRetryableSchedulesFuture(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)
	if err := s.CompleteAttempt(context.Background(), result(d, domain.OutcomeRetryable, 503, "http_503"), backoff.Default(), d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	st, nextAt := deliveryState(t, s, d.ID)
	if st != "retry_scheduled" {
		t.Fatalf("state = %s, want retry_scheduled", st)
	}
	if !nextAt.After(time.Now().Add(-time.Second)) {
		t.Fatalf("next_attempt_at %v not in the future-ish", nextAt)
	}
}

func TestAttemptRetryAfterHonored(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)
	r := result(d, domain.OutcomeRetryable, 429, "http_429")
	r.RetryAfter = 10 * time.Minute // > attempt-1 jitter ceiling (5s) → wins (§4)
	if err := s.CompleteAttempt(context.Background(), r, backoff.Default(), d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	_, nextAt := deliveryState(t, s, d.ID)
	if until := time.Until(nextAt); until < 9*time.Minute {
		t.Fatalf("next_attempt_at only %v away, Retry-After=10m not honored", until)
	}
}

func TestAttemptPermanentDeadLetters(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)
	if err := s.CompleteAttempt(context.Background(), result(d, domain.OutcomePermanent, 404, "http_404"), backoff.Default(), d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	st, _ := deliveryState(t, s, d.ID)
	if st != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered", st)
	}
	var finalErr string
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT final_error FROM dead_letters WHERE delivery_id = $1`, d.ID).Scan(&finalErr); err != nil {
		t.Fatalf("dead_letters row missing: %v", err)
	}
	if finalErr != "http_404" {
		t.Fatalf("final_error = %q, want http_404", finalErr)
	}
}

func TestAttemptExhaustionDeadLetters(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s)
	// fast-forward the budget so this IS the final attempt (fence must match)
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE deliveries SET attempt_count = $2 WHERE id = $1`, d.ID, d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	r := result(d, domain.OutcomeRetryable, 500, "http_500")
	r.AttemptNo = d.MaxAttempts // final attempt failed retryably → DLQ (§6)
	if err := s.CompleteAttempt(context.Background(), r, backoff.Default(), d.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	st, _ := deliveryState(t, s, d.ID)
	if st != "dead_lettered" {
		t.Fatalf("state = %s, want dead_lettered after max attempts", st)
	}
}

func TestStaleCompletionRejected(t *testing.T) {
	s := testStore(t)
	d := claimFresh(t, s) // worker A, attempt 1
	// A stalls; its lease expires; worker B takes over (attempt 2)
	setState(t, s, d.ID, "in_flight", time.Now().Add(-time.Minute))
	ok, d2, err := s.ClaimDelivery(context.Background(), d.ID, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("takeover: %v %v", ok, err)
	}
	// A wakes up with a stale 404 → must be rejected, NOT dead-lettered
	err = s.CompleteAttempt(context.Background(),
		result(d, domain.OutcomePermanent, 404, "http_404"), backoff.Default(), d.MaxAttempts)
	if !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("stale completion: got %v, want ErrStaleClaim", err)
	}
	st, _ := deliveryState(t, s, d.ID)
	if st != "in_flight" {
		t.Fatalf("state = %s after stale completion, want untouched in_flight", st)
	}
	var n int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id=$1`, d.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stale completion recorded %d attempt rows, want 0", n)
	}
	// the new owner's real completion still works
	if err := s.CompleteAttempt(context.Background(),
		result(d2, domain.OutcomeSuccess, 200, ""), backoff.Default(), d2.MaxAttempts); err != nil {
		t.Fatalf("new owner's completion failed: %v", err)
	}
}

func TestReplayDoesNotRecycleFencingTokens(t *testing.T) {
	s := testStore(t)
	id := mkDelivery(t, s)
	ctx := context.Background()

	// run 1, attempt 1 (version 1): retryable failure recorded
	ok, a, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim 1: %v %v", ok, err)
	}
	if err := s.CompleteAttempt(ctx, result(a, domain.OutcomeRetryable, 500, "http_500"),
		backoff.Default(), a.MaxAttempts); err != nil {
		t.Fatal(err)
	}
	// run 1, attempt 2 (version 2): permanent → dead-lettered
	if _, err := s.Pool.Exec(ctx,
		`UPDATE deliveries SET next_attempt_at = now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	ok, b, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim 2: %v %v", ok, err)
	}
	if err := s.CompleteAttempt(ctx, result(b, domain.OutcomePermanent, 410, "http_410"),
		backoff.Default(), b.MaxAttempts); err != nil {
		t.Fatal(err)
	}

	// manual replay (§6, simulated until the P1 admin API lands): the attempt
	// counter resets — but claim_version MUST NOT. That is the ABA defense.
	if _, err := s.Pool.Exec(ctx,
		`UPDATE deliveries SET state='pending', attempt_count=0, next_attempt_at=now() WHERE id=$1`,
		id); err != nil {
		t.Fatal(err)
	}

	// run 2: attempt_no is 1 AGAIN, but the fence is fresh (version 3)
	ok, c, err := s.ClaimDelivery(ctx, id, 30*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim after replay: %v %v", ok, err)
	}
	if c.AttemptCount != 1 || c.ClaimVersion != 3 {
		t.Fatalf("replayed claim: count=%d version=%d, want 1/3", c.AttemptCount, c.ClaimVersion)
	}
	// a stale completion from run 1 cannot clobber run 2 (token 1 ≠ 3)
	if err := s.CompleteAttempt(ctx, result(a, domain.OutcomeSuccess, 200, ""),
		backoff.Default(), a.MaxAttempts); !errors.Is(err, store.ErrStaleClaim) {
		t.Fatalf("run-1 token accepted after replay: %v", err)
	}
	// run 2's attempt_no 1 inserts fine: uniqueness is per claim_version
	if err := s.CompleteAttempt(ctx, result(c, domain.OutcomeSuccess, 200, ""),
		backoff.Default(), c.MaxAttempts); err != nil {
		t.Fatalf("replayed run completion failed: %v", err)
	}
}
