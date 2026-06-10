//go:build integration

package store_test

import (
	"context"
	"testing"
)

func masterKey() [32]byte { var k [32]byte; copy(k[:], "0123456789abcdef0123456789abcdef"); return k }

func TestMigrateUpDown(t *testing.T) {
	s := testStore(t)
	// up already ran in testStore; verify a core table exists
	var n int
	if err := s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM deliveries`).Scan(&n); err != nil {
		t.Fatalf("deliveries table missing after up: %v", err)
	}
	if err := s.MigrateDown(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}

func TestSeedHelpersRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	keyID, plaintext, err := s.CreateProducerKey(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	gotID, err := s.LookupProducerKey(ctx, plaintext)
	if err != nil || gotID != keyID {
		t.Fatalf("lookup by hash: got (%q, %v), want %q", gotID, err, keyID)
	}
	if _, err := s.LookupProducerKey(ctx, "hk_wrong"); err == nil {
		t.Fatal("wrong key resolved")
	}
	epID, secret, err := s.CreateEndpoint(ctx, masterKey(), "https://consumer.example/hook", "test ep")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" || epID == "" {
		t.Fatal("empty endpoint id or secret")
	}
	if _, err := s.CreateSubscription(ctx, "orders.*", epID, 8); err != nil {
		t.Fatal(err)
	}
}
