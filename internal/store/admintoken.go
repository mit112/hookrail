package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminTokenRow is the metadata view of an admin token (never the hash/plaintext).
type AdminTokenRow struct {
	ID        string
	Role      string
	Label     string
	CreatedAt time.Time
	RevokedAt *time.Time
}

// CreateAdminToken stores a new RBAC token hashed and returns (id, plaintext).
// The plaintext is shown once and never stored.
func (s *Store) CreateAdminToken(ctx context.Context, role, label string) (id, plaintext string, err error) {
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	plaintext = "hkadm_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plaintext))
	id = NewID()
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO admin_tokens (id, token_hash, role, label) VALUES ($1, $2, $3, $4)`,
		id, hash[:], role, label)
	return id, plaintext, err
}

// CreateAdminTokenCapped atomically creates a token only if the active
// (un-revoked) count is below max, avoiding the TOCTOU of a separate
// count-then-insert. capped=true (no row inserted) when the cap is already
// reached. The cap is an anti-sprawl bound; a sub-statement race under
// simultaneous creates could overshoot by a few, which is acceptable.
func (s *Store) CreateAdminTokenCapped(ctx context.Context, role, label string, max int) (id, plaintext string, capped bool, err error) {
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", "", false, err
	}
	plaintext = "hkadm_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plaintext))
	id = NewID()
	ct, err := s.Pool.Exec(ctx,
		`INSERT INTO admin_tokens (id, token_hash, role, label)
		 SELECT $1, $2, $3, $4
		 WHERE (SELECT count(*) FROM admin_tokens WHERE revoked_at IS NULL) < $5`,
		id, hash[:], role, label, max)
	if err != nil {
		return "", "", false, err
	}
	if ct.RowsAffected() == 0 {
		return "", "", true, nil
	}
	return id, plaintext, false, nil
}

// LookupAdminToken resolves a presented plaintext token to (id, role) by hash.
// Returns ErrNotFound when there is no active (un-revoked) match.
func (s *Store) LookupAdminToken(ctx context.Context, plaintext string) (string, string, error) {
	hash := sha256.Sum256([]byte(plaintext))
	var id, role string
	err := s.Pool.QueryRow(ctx,
		`SELECT id, role FROM admin_tokens WHERE token_hash = $1 AND revoked_at IS NULL`,
		hash[:]).Scan(&id, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return id, role, err
}

// ListAdminTokens returns a keyset page (ascending id) of token metadata.
func (s *Store) ListAdminTokens(ctx context.Context, cursor string, limit int) ([]AdminTokenRow, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, role, label, created_at, revoked_at
		   FROM admin_tokens
		  WHERE ($1 = '' OR id > $1)
		  ORDER BY id ASC
		  LIMIT $2`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AdminTokenRow, 0, limit)
	for rows.Next() {
		var r AdminTokenRow
		if err := rows.Scan(&r.ID, &r.Role, &r.Label, &r.CreatedAt, &r.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokeAdminToken soft-revokes an active token. found=false if no active row matched.
func (s *Store) RevokeAdminToken(ctx context.Context, id string) (bool, error) {
	ct, err := s.Pool.Exec(ctx,
		`UPDATE admin_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// CountActiveAdminTokens counts un-revoked tokens (for the active-token cap).
func (s *Store) CountActiveAdminTokens(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM admin_tokens WHERE revoked_at IS NULL`).Scan(&n)
	return n, err
}

// AdminTokenExists reports whether a row with this id exists (revoked or not).
func (s *Store) AdminTokenExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM admin_tokens WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}
