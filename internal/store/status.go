package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type AttemptView struct {
	AttemptNo    int    `json:"attempt_no"`     // resets on replay (§6)
	ClaimVersion int64  `json:"claim_version"`  // never resets — the true timeline order
	Status       string `json:"status"`
	HTTPStatus   *int   `json:"http_status,omitempty"`
	ErrorClass   *string `json:"error_class,omitempty"`
	LatencyMS    int    `json:"latency_ms"`
}

type DeliveryView struct {
	DeliveryID        string        `json:"delivery_id"`
	State             string        `json:"state"`
	AttemptsTruncated bool          `json:"attempts_truncated"`
	Attempts          []AttemptView `json:"attempts"`
}

type EventStatus struct {
	EventID    string         `json:"event_id"`
	Topic      string         `json:"topic"`
	Deliveries []DeliveryView `json:"deliveries"`
}

// GetEventStatus backs GET /v1/events/{id} (§9): per-delivery attempt timeline.
func (s *Store) GetEventStatus(ctx context.Context, eventID string) (EventStatus, error) {
	var st EventStatus
	if err := s.Pool.QueryRow(ctx,
		`SELECT id, topic FROM events WHERE id = $1`, eventID).Scan(&st.EventID, &st.Topic); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return st, ErrNotFound
		}
		return st, err
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT id, state::text, (attempts_truncated_at IS NOT NULL) FROM deliveries WHERE event_id = $1 ORDER BY id`, eventID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DeliveryView
		if err := rows.Scan(&d.DeliveryID, &d.State, &d.AttemptsTruncated); err != nil {
			return st, err
		}
		d.Attempts = []AttemptView{}
		st.Deliveries = append(st.Deliveries, d)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for i := range st.Deliveries {
		// ordered by claim_version, not attempt_no: replay resets attempt
		// numbers (§6), so attempt_no is ambiguous across runs
		arows, err := s.Pool.Query(ctx,
			`SELECT attempt_no, claim_version, status::text, http_status, error_class, latency_ms
			 FROM delivery_attempts WHERE delivery_id = $1 ORDER BY claim_version`,
			st.Deliveries[i].DeliveryID)
		if err != nil {
			return st, err
		}
		for arows.Next() {
			var a AttemptView
			if err := arows.Scan(&a.AttemptNo, &a.ClaimVersion, &a.Status, &a.HTTPStatus, &a.ErrorClass, &a.LatencyMS); err != nil {
				arows.Close()
				return st, err
			}
			st.Deliveries[i].Attempts = append(st.Deliveries[i].Attempts, a)
		}
		arows.Close()
	}
	return st, nil
}
