package store

import (
	"context"
	"time"
)

// ReleaseClaimForDrain fenced-releases an in-flight delivery so another replica
// can pick it up during graceful drain.  Unlike the rate-limit ReleaseClaim:
//   - It does NOT decrement attempt_count (no attempt was consumed).
//   - It does NOT touch claim_version (the fencing token stays monotonic).
//   - It sets state=retry_scheduled with a near-future next_attempt_at,
//     NEVER in_flight+NULL lease (that would strand the row forever).
//
// 0 rows affected (state changed, stale claim_version, or row doesn't exist)
// is NOT an error — another owner already took it.
func (s *Store) ReleaseClaimForDrain(ctx context.Context, id string, claimVersion int64, jitter time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE deliveries
			SET state='retry_scheduled',
			    next_attempt_at = now() + $3,
			    lease_until = NULL,
			    updated_at = now()
		  WHERE id=$1 AND state='in_flight' AND claim_version=$2`,
		id, claimVersion, jitter)
	return err
}
