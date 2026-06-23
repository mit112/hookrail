package store

import (
	"context"
	"errors"
	"fmt"
)

// ErrTopicForbidden: a producer key tried to publish a topic outside its scope
// (handler → 403). Distinct from authentication failure (401).
var ErrTopicForbidden = errors.New("store: producer key not authorized for topic")

// ErrNoScopes: refused to mint a producer key with no topic scopes — such a key
// is unusable under deny-when-unscoped.
var ErrNoScopes = errors.New("store: producer key requires at least one topic scope")

const maxTopicPatternLen = 255

// ValidateTopicPattern accepts MatchTopic patterns: non-empty, <=255 bytes, no
// ASCII whitespace/control characters. "*" and "foo.*" are valid.
func ValidateTopicPattern(p string) error {
	if p == "" {
		return errors.New("store: topic pattern is empty")
	}
	if len(p) > maxTopicPatternLen {
		return fmt.Errorf("store: topic pattern exceeds %d bytes", maxTopicPatternLen)
	}
	for _, r := range p {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("store: topic pattern contains whitespace/control char")
		}
	}
	return nil
}

// dedupePatterns returns a deterministic, de-duplicated copy preserving first-seen order.
func dedupePatterns(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// AuthorizeProducerTopic returns nil if producerKeyID may publish to topic,
// ErrTopicForbidden if not (incl. zero scope rows — fail-closed), or a DB error.
func (s *Store) AuthorizeProducerTopic(ctx context.Context, producerKeyID, topic string) error {
	rows, err := s.Pool.Query(ctx,
		`SELECT topic_pattern FROM producer_key_scopes WHERE producer_key_id = $1`,
		producerKeyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	allowed := false
	for rows.Next() {
		var pat string
		if err := rows.Scan(&pat); err != nil {
			return err
		}
		if MatchTopic(pat, topic) {
			allowed = true
			// keep draining rows so the connection is reusable
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !allowed {
		return ErrTopicForbidden
	}
	return nil
}
