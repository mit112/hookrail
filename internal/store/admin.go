package store

import (
	"context"
	"time"
)

type EndpointRow struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

func (s *Store) GetEndpoint(ctx context.Context, id string, includeDeleted bool) (EndpointRow, error) {
	q := `SELECT id, url, description, created_at, deleted_at FROM endpoints WHERE id=$1`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	var e EndpointRow
	err := s.Pool.QueryRow(ctx, q, id).Scan(&e.ID, &e.URL, &e.Description, &e.CreatedAt, &e.DeletedAt)
	return e, err
}

// ListEndpoints is keyset-paginated on the immutable id (design §2.1): DESC,
// id < cursor. afterID == "" starts at the newest — expressed as
// `($1 = '' OR id < $1)` so it is collation-independent (no max-char sentinel).
func (s *Store) ListEndpoints(ctx context.Context, afterID string, limit int, includeDeleted bool) ([]EndpointRow, error) {
	q := `SELECT id, url, description, created_at, deleted_at FROM endpoints WHERE ($1 = '' OR id < $1)`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.URL, &e.Description, &e.CreatedAt, &e.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpdateEndpoint applies a PARTIAL update: nil pointer = leave the column
// unchanged (COALESCE), so a description-only PATCH never clobbers the URL and
// vice versa.
func (s *Store) UpdateEndpoint(ctx context.Context, id string, url, description *string) error {
	ct, err := s.Pool.Exec(ctx,
		`UPDATE endpoints SET
		   url = COALESCE($2, url),
		   description = COALESCE($3, description)
		 WHERE id=$1 AND deleted_at IS NULL`, id, url, description)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SoftDeleteEndpoint marks the endpoint and its subscriptions deleted in one tx.
// Delivery cancellation is added to this method in M-A4a (Task 15).
func (s *Store) SoftDeleteEndpoint(ctx context.Context, id string) error {
	tx, err := s.Pool.BeginTx(ctx, pgxTxRW())
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	ct, err := tx.Exec(ctx, `UPDATE endpoints SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx,
		`UPDATE subscriptions SET deleted_at=now() WHERE endpoint_id=$1 AND deleted_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
