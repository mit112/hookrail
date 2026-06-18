package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"

	hcrypto "github.com/mit112/hookrail/internal/crypto"
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

// ── Subscription types & store methods ──────────────────────────────────────

type SubInput struct {
	TopicPattern  string
	EndpointID    string
	MaxAttempts   int
	RateLimitRPS  *float64
	BackoffPolicy []byte // raw JSONB or nil
}

type SubscriptionRow struct {
	ID            string          `json:"id"`
	TopicPattern  string          `json:"topic_pattern"`
	EndpointID    string          `json:"endpoint_id"`
	MaxAttempts   int             `json:"max_attempts"`
	RateLimitRPS  *float64        `json:"rate_limit_rps,omitempty"`
	BackoffPolicy json.RawMessage `json:"backoff_policy,omitempty"`
	Active        bool            `json:"active"`
	DeletedAt     *time.Time      `json:"deleted_at,omitempty"`
}

// CreateSubscriptionFull inserts a subscription against a LIVE endpoint only.
// A deleted/absent endpoint yields ErrConflict (handler → 409). CHECK
// constraints reject bad max_attempts/rate (handler maps the PG error → 422).
func (s *Store) CreateSubscriptionFull(ctx context.Context, p SubInput) (string, error) {
	id := NewID()
	ct, err := s.Pool.Exec(ctx,
		`INSERT INTO subscriptions (id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy)
		 SELECT $1, $2, $3, $4, $5, $6
		 WHERE EXISTS (SELECT 1 FROM endpoints WHERE id=$3 AND deleted_at IS NULL)`,
		id, p.TopicPattern, p.EndpointID, p.MaxAttempts, p.RateLimitRPS, nullJSON(p.BackoffPolicy))
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", ErrConflict
	}
	return id, nil
}

func (s *Store) GetSubscription(ctx context.Context, id string, includeDeleted bool) (SubscriptionRow, error) {
	q := `SELECT id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy, active, deleted_at
	      FROM subscriptions WHERE id=$1`
	if !includeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	var r SubscriptionRow
	err := s.Pool.QueryRow(ctx, q, id).Scan(&r.ID, &r.TopicPattern, &r.EndpointID, &r.MaxAttempts,
		&r.RateLimitRPS, &r.BackoffPolicy, &r.Active, &r.DeletedAt)
	return r, err
}

func (s *Store) ListSubscriptions(ctx context.Context, endpointID, afterID string, limit int) ([]SubscriptionRow, error) {
	args := []any{afterID, limit}
	q := `SELECT id, topic_pattern, endpoint_id, max_attempts, rate_limit_rps, backoff_policy, active, deleted_at
	      FROM subscriptions WHERE ($1 = '' OR id < $1) AND deleted_at IS NULL`
	if endpointID != "" {
		q += ` AND endpoint_id = $3`
		args = append(args, endpointID)
	}
	q += ` ORDER BY id DESC LIMIT $2`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscriptionRow
	for rows.Next() {
		var r SubscriptionRow
		if err := rows.Scan(&r.ID, &r.TopicPattern, &r.EndpointID, &r.MaxAttempts,
			&r.RateLimitRPS, &r.BackoffPolicy, &r.Active, &r.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateSubscription applies partial updates. A soft-deleted subscription is
// immutable (F3): 0 rows affected → ErrNotFound, which the handler maps to 409
// when the row exists-but-deleted, else 404.
func (s *Store) UpdateSubscription(ctx context.Context, id string, active *bool, maxAttempts *int, rps *float64, backoff []byte, setBackoff bool) error {
	ct, err := s.Pool.Exec(ctx,
		`UPDATE subscriptions SET
		   active = COALESCE($2, active),
		   max_attempts = COALESCE($3, max_attempts),
		   rate_limit_rps = CASE WHEN $4 THEN $5 ELSE rate_limit_rps END,
		   backoff_policy = CASE WHEN $6 THEN $7 ELSE backoff_policy END
		 WHERE id=$1 AND deleted_at IS NULL`,
		id, active, maxAttempts, rps != nil, rps, setBackoff, nullJSON(backoff))
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SoftDeleteSubscription(ctx context.Context, id string) error {
	ct, err := s.Pool.Exec(ctx, `UPDATE subscriptions SET deleted_at=now() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SubscriptionExists checks if a subscription with the given id exists (including soft-deleted).
func (s *Store) SubscriptionExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE id=$1)`, id).Scan(&ok)
	return ok, err
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// RotateEndpointSecret generates a fresh whsec_ secret, encrypts it with the
// master key, and atomically swaps the endpoint's secret_ciphertext. Returns
// the plaintext secret once; caller must set Cache-Control: no-store.
// A deleted/absent endpoint yields ErrNotFound (handler → 404).
func (s *Store) RotateEndpointSecret(ctx context.Context, masterKey [32]byte, id string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	secret := "whsec_" + hex.EncodeToString(raw)
	box, err := hcrypto.Encrypt(masterKey, []byte(secret))
	if err != nil {
		return "", err
	}
	ct, err := s.Pool.Exec(ctx,
		`UPDATE endpoints SET secret_ciphertext=$2 WHERE id=$1 AND deleted_at IS NULL`, id, box)
	if err != nil {
		return "", err
	}
	if ct.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}
