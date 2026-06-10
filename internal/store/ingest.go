package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrIdempotencyConflict: same idempotency key, different body hash → HTTP 409 (§4).
var ErrIdempotencyConflict = errors.New("store: idempotency key reused with different body")

type IngestParams struct {
	ProducerKeyID string
	Topic         string
	Payload       []byte
	IdemKey       string        // optional
	IdemTTL       time.Duration // ignored when IdemKey == ""
}

type IngestResult struct {
	EventID     string   `json:"event_id"`
	DeliveryIDs []string `json:"delivery_ids"`
	Replayed    bool     `json:"-"`
}

// IngestEvent is the §3.1 fast path: ONE transaction inserts the event and
// one deliveries row per matching active subscription (the durable fan-out),
// plus the idempotency row. The caller XADDs after commit (best-effort).
func (s *Store) IngestEvent(ctx context.Context, p IngestParams) (IngestResult, error) {
	// hash the canonical logical request — topic AND payload — so the same
	// key with the same payload but a different topic conflicts (409)
	// rather than silently replaying the wrong event
	h := sha256.New()
	h.Write([]byte(p.Topic))
	h.Write([]byte{0})
	h.Write(p.Payload)
	bodyHash := h.Sum(nil)

	// Fast path: replay/conflict check outside the insert txn.
	if p.IdemKey != "" {
		if res, found, err := s.lookupIdem(ctx, p, bodyHash); err != nil || found {
			return res, err
		}
	}

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return IngestResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	eventID := NewID()
	if _, err := tx.Exec(ctx,
		`INSERT INTO events (id, producer_key_id, topic, payload, payload_size)
		 VALUES ($1, $2, $3, $4, $5)`,
		eventID, p.ProducerKeyID, p.Topic, p.Payload, len(p.Payload)); err != nil {
		return IngestResult{}, fmt.Errorf("insert event: %w", err)
	}

	// Topic matching evaluated at ingest (§3.1). Subscription counts are
	// small in P0; match in Go for clarity.
	rows, err := tx.Query(ctx,
		`SELECT id, topic_pattern FROM subscriptions WHERE active`)
	if err != nil {
		return IngestResult{}, err
	}
	type sub struct{ id, pattern string }
	var subs []sub
	for rows.Next() {
		var x sub
		if err := rows.Scan(&x.id, &x.pattern); err != nil {
			rows.Close()
			return IngestResult{}, err
		}
		subs = append(subs, x)
	}
	rows.Close()

	var deliveryIDs []string
	for _, x := range subs {
		if !MatchTopic(x.pattern, p.Topic) {
			continue
		}
		did := NewID()
		if _, err := tx.Exec(ctx,
			`INSERT INTO deliveries (id, event_id, subscription_id) VALUES ($1, $2, $3)`,
			did, eventID, x.id); err != nil {
			return IngestResult{}, fmt.Errorf("insert delivery: %w", err)
		}
		deliveryIDs = append(deliveryIDs, did)
	}

	res := IngestResult{EventID: eventID, DeliveryIDs: deliveryIDs}

	if p.IdemKey != "" {
		snapshot, err := json.Marshal(res)
		if err != nil {
			return IngestResult{}, err
		}
		// expired rows still occupy the PK: take them over atomically.
		// DO UPDATE fires only when the existing row is expired; a live row
		// yields 0 affected rows → concurrent/duplicate request path below.
		ct, err := tx.Exec(ctx,
			`INSERT INTO idempotency_keys
			   (producer_key_id, idem_key, body_hash, event_id, response_snapshot, expires_at)
			 VALUES ($1, $2, $3, $4, $5, now() + $6)
			 ON CONFLICT (producer_key_id, idem_key) DO UPDATE
			   SET body_hash = EXCLUDED.body_hash,
			       event_id = EXCLUDED.event_id,
			       response_snapshot = EXCLUDED.response_snapshot,
			       expires_at = EXCLUDED.expires_at
			   WHERE idempotency_keys.expires_at <= now()`,
			p.ProducerKeyID, p.IdemKey, bodyHash, eventID, snapshot, p.IdemTTL)
		if err != nil {
			return IngestResult{}, err
		}
		if ct.RowsAffected() == 0 {
			// A concurrent request won the key. Abandon our event (rollback)
			// and serve theirs — this is the concurrent-duplicate race (§11).
			if err := tx.Rollback(ctx); err != nil {
				return IngestResult{}, err
			}
			res, found, err := s.lookupIdem(ctx, p, bodyHash)
			if err != nil {
				return IngestResult{}, err
			}
			if !found {
				// live row expired in the microseconds since the conflict —
				// extremely rare; surface as retryable
				return IngestResult{}, errors.New("store: idempotency race, retry request")
			}
			return res, nil
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return IngestResult{}, err
	}
	return res, nil
}

// lookupIdem returns (snapshot, true, nil) on replay, (zero, false, nil) when
// absent/expired, ErrIdempotencyConflict on body-hash mismatch.
func (s *Store) lookupIdem(ctx context.Context, p IngestParams, bodyHash []byte) (IngestResult, bool, error) {
	var storedHash []byte
	var snapshot []byte
	err := s.Pool.QueryRow(ctx,
		`SELECT body_hash, response_snapshot FROM idempotency_keys
		 WHERE producer_key_id = $1 AND idem_key = $2 AND expires_at > now()`,
		p.ProducerKeyID, p.IdemKey).Scan(&storedHash, &snapshot)
	if errors.Is(err, pgx.ErrNoRows) {
		return IngestResult{}, false, nil
	}
	if err != nil {
		return IngestResult{}, false, err
	}
	if !bytesEqual(storedHash, bodyHash) {
		return IngestResult{}, false, ErrIdempotencyConflict
	}
	var res IngestResult
	if err := json.Unmarshal(snapshot, &res); err != nil {
		return IngestResult{}, false, err
	}
	res.Replayed = true
	return res, true, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
