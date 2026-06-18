package store

import (
	"context"
	"strconv"
	"time"
)

type DLQFilter struct {
	AfterID    int64 // 0 = from newest
	Limit      int
	EndpointID string
	Replayed   *bool
	Since      *time.Time
	Until      *time.Time
}

type DLQRow struct {
	ID         int64      `json:"-"`
	DeliveryID string     `json:"delivery_id"`
	EndpointID *string    `json:"endpoint_id,omitempty"`
	FinalError string     `json:"final_error"`
	DeadAt     time.Time  `json:"dead_at"`
	ReplayedAt *time.Time `json:"replayed_at,omitempty"`
}

// ListDLQ pages dead_letters keyset on the bigserial id DESC (immutable;
// never dead_at — re-dead-letter rewrites it, design §2.1).
func (s *Store) ListDLQ(ctx context.Context, f DLQFilter) ([]DLQRow, error) {
	// AfterID == 0 means "from the newest"; the bigserial id is always > 0.
	q := `SELECT id, delivery_id, endpoint_id, final_error, dead_at, replayed_at
	      FROM dead_letters WHERE ($1 = 0 OR id < $1)`
	args := []any{f.AfterID, f.Limit}
	add := func(cond string, v any) { args = append(args, v); q += cond + strconv.Itoa(len(args)) }
	if f.EndpointID != "" {
		add(" AND endpoint_id = $", f.EndpointID)
	}
	if f.Replayed != nil {
		if *f.Replayed {
			q += " AND replayed_at IS NOT NULL"
		} else {
			q += " AND replayed_at IS NULL"
		}
	}
	if f.Since != nil {
		add(" AND dead_at >= $", *f.Since)
	}
	if f.Until != nil {
		add(" AND dead_at < $", *f.Until)
	}
	q += " ORDER BY id DESC LIMIT $2"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DLQRow
	for rows.Next() {
		var d DLQRow
		if err := rows.Scan(&d.ID, &d.DeliveryID, &d.EndpointID, &d.FinalError, &d.DeadAt, &d.ReplayedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
