//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestCreateProducerKeyRequiresScope(t *testing.T) {
	s := testStore(t)
	if _, _, err := s.CreateProducerKey(context.Background(), "noscope", nil); !errors.Is(err, store.ErrNoScopes) {
		t.Fatalf("err = %v, want ErrNoScopes", err)
	}
}

func TestCreateProducerKeyWritesScopesAtomically(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id, _, err := s.CreateProducerKey(ctx, "scoped", []string{"orders.*", "orders.*", "payments.captured"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM producer_key_scopes WHERE producer_key_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 { // de-duped
		t.Fatalf("scope rows = %d, want 2 (deduped)", n)
	}
}

func TestCreateProducerKeyRejectsBadPatternNoPartialKey(t *testing.T) {
	// A bad pattern must roll back the whole transaction: no key row is left.
	s := testStore(t)
	ctx := context.Background()
	if _, _, err := s.CreateProducerKey(ctx, "bad", []string{"orders.*", "has space"}); err == nil {
		t.Fatal("expected validation error for 'has space'")
	}
	var n int
	if err := s.Pool.QueryRow(ctx,
		`SELECT count(*) FROM producer_keys WHERE name='bad'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("producer_keys rows = %d, want 0 (no partial key on bad pattern)", n)
	}
}

func TestAuthorizeProducerTopic(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	id, _, err := s.CreateProducerKey(ctx, "auth", []string{"orders.*"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeProducerTopic(ctx, id, "orders.created"); err != nil {
		t.Errorf("orders.created denied: %v", err)
	}
	if err := s.AuthorizeProducerTopic(ctx, id, "payments.x"); !errors.Is(err, store.ErrTopicForbidden) {
		t.Errorf("payments.x err = %v, want ErrTopicForbidden", err)
	}
}

func TestAuthorizeProducerTopicMatchAny(t *testing.T) {
	// Match-any across multiple disjoint patterns.
	s := testStore(t)
	ctx := context.Background()
	id, _, err := s.CreateProducerKey(ctx, "multi", []string{"orders.*", "payments.captured"})
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{"orders.created", "payments.captured"} {
		if err := s.AuthorizeProducerTopic(ctx, id, topic); err != nil {
			t.Errorf("%s denied: %v", topic, err)
		}
	}
	if err := s.AuthorizeProducerTopic(ctx, id, "payments.refunded"); !errors.Is(err, store.ErrTopicForbidden) {
		t.Errorf("payments.refunded err = %v, want ErrTopicForbidden", err)
	}
}

func TestAuthorizeProducerTopicEmptyScopeDenied(t *testing.T) {
	// FAIL-CLOSED: a key with zero scope rows must be denied. This guards
	// against a regression where empty = allow-all.
	s := testStore(t)
	ctx := context.Background()
	id := store.NewID()
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO producer_keys (id, key_hash, name) VALUES ($1, $2, $3)`,
		id, []byte("emptyscope-hash-bbbbbbbbbbbbbbbb"), "empty"); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeProducerTopic(ctx, id, "anything"); !errors.Is(err, store.ErrTopicForbidden) {
		t.Fatalf("empty-scope key err = %v, want ErrTopicForbidden (fail-closed)", err)
	}
}
