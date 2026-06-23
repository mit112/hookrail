package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/jackc/pgx/v5"

	hcrypto "github.com/mit112/hookrail/internal/crypto"
)

// CreateProducerKey stores a new key hashed (§4) plus its topic scopes, in one
// transaction, and returns (id, plaintext). At least one scope is required;
// each pattern is validated and de-duplicated. The plaintext is shown once and
// never stored.
func (s *Store) CreateProducerKey(ctx context.Context, name string, scopes []string) (id, plaintext string, err error) {
	scopes = dedupePatterns(scopes)
	if len(scopes) == 0 {
		return "", "", ErrNoScopes
	}
	for _, p := range scopes {
		if err = ValidateTopicPattern(p); err != nil {
			return "", "", err
		}
	}
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	plaintext = "hk_" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(plaintext))
	id = NewID()

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	if _, err = tx.Exec(ctx,
		`INSERT INTO producer_keys (id, key_hash, name) VALUES ($1, $2, $3)`,
		id, hash[:], name); err != nil {
		return "", "", err
	}
	for _, p := range scopes {
		if _, err = tx.Exec(ctx,
			`INSERT INTO producer_key_scopes (producer_key_id, topic_pattern) VALUES ($1, $2)`,
			id, p); err != nil {
			return "", "", err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", err
	}
	return id, plaintext, nil
}

// LookupProducerKey resolves a presented plaintext key to its id by hash.
func (s *Store) LookupProducerKey(ctx context.Context, plaintext string) (string, error) {
	hash := sha256.Sum256([]byte(plaintext))
	var id string
	err := s.Pool.QueryRow(ctx,
		`SELECT id FROM producer_keys WHERE key_hash = $1 AND revoked_at IS NULL`,
		hash[:]).Scan(&id)
	return id, err
}

// CreateEndpoint encrypts the HMAC secret at rest and returns (id, plaintext secret).
func (s *Store) CreateEndpoint(ctx context.Context, masterKey [32]byte, url, description string) (id, secret string, err error) {
	raw := make([]byte, 24)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	secret = "whsec_" + hex.EncodeToString(raw)
	box, err := hcrypto.Encrypt(masterKey, []byte(secret))
	if err != nil {
		return "", "", err
	}
	id = NewID()
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO endpoints (id, url, secret_ciphertext, description) VALUES ($1, $2, $3, $4)`,
		id, url, box, description)
	return id, secret, err
}

func (s *Store) CreateSubscription(ctx context.Context, topicPattern, endpointID string, maxAttempts int) (string, error) {
	id := NewID()
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO subscriptions (id, topic_pattern, endpoint_id, max_attempts) VALUES ($1, $2, $3, $4)`,
		id, topicPattern, endpointID, maxAttempts)
	return id, err
}
