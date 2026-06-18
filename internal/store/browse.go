package store

import (
	"context"
	"strconv"
)

type DeliveryFilter struct {
	AfterID    string
	Limit      int
	State      string
	EndpointID string
	Topic      string
	EventID    string
}

type DeliveryListRow struct {
	ID         string `json:"id"`
	EventID    string `json:"event_id"`
	EndpointID string `json:"endpoint_id"`
	State      string `json:"state"`
}

func (s *Store) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]DeliveryListRow, error) {
	q := `SELECT d.id, d.event_id, d.endpoint_id, d.state::text
	      FROM deliveries d JOIN events e ON e.id = d.event_id
	      WHERE ($1 = '' OR d.id < $1)`
	args := []any{f.AfterID, f.Limit}
	add := func(cond string, v any) { args = append(args, v); q += cond + strconv.Itoa(len(args)) }
	if f.State != "" {
		add(" AND d.state = $", f.State+"") // cast below
		q += "::delivery_state"
	}
	if f.EndpointID != "" {
		add(" AND d.endpoint_id = $", f.EndpointID)
	}
	if f.Topic != "" {
		add(" AND e.topic = $", f.Topic)
	}
	if f.EventID != "" {
		add(" AND d.event_id = $", f.EventID)
	}
	q += " ORDER BY d.id DESC LIMIT $2"
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeliveryListRow
	for rows.Next() {
		var d DeliveryListRow
		if err := rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.State); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type DeliveryTimeline struct {
	DeliveryID        string        `json:"delivery_id"`
	State             string        `json:"state"`
	AttemptsTruncated bool          `json:"attempts_truncated"`
	Attempts          []AttemptView `json:"attempts"`
}

func (s *Store) GetDeliveryTimeline(ctx context.Context, id string) (DeliveryTimeline, error) {
	var tl DeliveryTimeline
	var truncatedAt *string
	if err := s.Pool.QueryRow(ctx,
		`SELECT id, state::text, (attempts_truncated_at IS NOT NULL)::text
		 FROM deliveries WHERE id=$1`, id).Scan(&tl.DeliveryID, &tl.State, &truncatedAt); err != nil {
		return tl, err
	}
	tl.AttemptsTruncated = truncatedAt != nil && *truncatedAt == "true"
	rows, err := s.Pool.Query(ctx,
		`SELECT attempt_no, claim_version, status::text, http_status, error_class, latency_ms
		 FROM delivery_attempts WHERE delivery_id=$1 ORDER BY claim_version`, id)
	if err != nil {
		return tl, err
	}
	defer rows.Close()
	tl.Attempts = []AttemptView{}
	for rows.Next() {
		var a AttemptView
		if err := rows.Scan(&a.AttemptNo, &a.ClaimVersion, &a.Status, &a.HTTPStatus, &a.ErrorClass, &a.LatencyMS); err != nil {
			return tl, err
		}
		tl.Attempts = append(tl.Attempts, a)
	}
	return tl, rows.Err()
}
