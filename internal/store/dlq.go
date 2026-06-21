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
	ID          int64      `json:"-"`
	DeliveryID  string     `json:"delivery_id"`
	EndpointID  *string    `json:"endpoint_id,omitempty"`
	FinalError  string     `json:"final_error"`
	DeadAt      time.Time  `json:"dead_at"`
	ReplayedAt  *time.Time `json:"replayed_at,omitempty"`
	OrderingKey *string    `json:"ordering_key,omitempty"` // NULL for unordered deliveries
}

// ListDLQ pages dead_letters keyset on the bigserial id DESC (immutable;
// never dead_at — re-dead-letter rewrites it, design §2.1). Joins deliveries to
// surface ordering_key (NULL for the unordered path).
func (s *Store) ListDLQ(ctx context.Context, f DLQFilter) ([]DLQRow, error) {
	// AfterID == 0 means "from the newest"; the bigserial id is always > 0.
	q := `SELECT dl.id, dl.delivery_id, dl.endpoint_id, dl.final_error, dl.dead_at, dl.replayed_at, d.ordering_key
	      FROM dead_letters dl JOIN deliveries d ON d.id = dl.delivery_id
	      WHERE ($1 = 0 OR dl.id < $1)`
	args := []any{f.AfterID, f.Limit}
	add := func(cond string, v any) { args = append(args, v); q += cond + strconv.Itoa(len(args)) }
	if f.EndpointID != "" {
		add(" AND dl.endpoint_id = $", f.EndpointID)
	}
	if f.Replayed != nil {
		if *f.Replayed {
			q += " AND dl.replayed_at IS NOT NULL"
		} else {
			q += " AND dl.replayed_at IS NULL"
		}
	}
	if f.Since != nil {
		add(" AND dl.dead_at >= $", *f.Since)
	}
	if f.Until != nil {
		add(" AND dl.dead_at < $", *f.Until)
	}
	q += " ORDER BY dl.id DESC LIMIT $2"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DLQRow
	for rows.Next() {
		var d DLQRow
		if err := rows.Scan(&d.ID, &d.DeliveryID, &d.EndpointID, &d.FinalError, &d.DeadAt, &d.ReplayedAt, &d.OrderingKey); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
