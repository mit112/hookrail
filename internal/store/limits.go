package store

import "context"

// EndpointRateLimits returns the effective per-endpoint rate cap: the MIN
// rate_limit_rps across an endpoint's active, non-deleted subscriptions that
// set one. Endpoints with no rps-bearing sub are absent (keep the default).
func (s *Store) EndpointRateLimits(ctx context.Context) (map[string]float64, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT endpoint_id, MIN(rate_limit_rps)
		 FROM subscriptions
		 WHERE active AND deleted_at IS NULL AND rate_limit_rps IS NOT NULL
		 GROUP BY endpoint_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var ep string
		var rps float64
		if err := rows.Scan(&ep, &rps); err != nil {
			return nil, err
		}
		out[ep] = rps
	}
	return out, rows.Err()
}
