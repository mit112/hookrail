//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestAdminTokenRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	id, plain, err := s.CreateAdminToken(ctx, "operator", "ci-replayer")
	if err != nil {
		t.Fatal(err)
	}
	if plain[:6] != "hkadm_" {
		t.Fatalf("token prefix = %q, want hkadm_", plain[:6])
	}

	gotID, role, err := s.LookupAdminToken(ctx, plain)
	if err != nil || gotID != id || role != "operator" {
		t.Fatalf("lookup = (%q,%q,%v), want (%q,operator,nil)", gotID, role, err, id)
	}

	if _, _, err := s.LookupAdminToken(ctx, "hkadm_wrong"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("wrong token err = %v, want ErrNotFound", err)
	}

	n, err := s.CountActiveAdminTokens(ctx)
	if err != nil || n != 1 {
		t.Fatalf("count active = (%d,%v), want (1,nil)", n, err)
	}

	rows, err := s.ListAdminTokens(ctx, "", 50)
	if err != nil || len(rows) != 1 || rows[0].Role != "operator" || rows[0].RevokedAt != nil {
		t.Fatalf("list = (%+v,%v)", rows, err)
	}

	found, err := s.RevokeAdminToken(ctx, id)
	if err != nil || !found {
		t.Fatalf("revoke = (%v,%v), want (true,nil)", found, err)
	}
	// Revoked token no longer resolves.
	if _, _, err := s.LookupAdminToken(ctx, plain); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revoked lookup err = %v, want ErrNotFound", err)
	}
	// Re-revoke is a no-op that reports not-found-active.
	if found, _ := s.RevokeAdminToken(ctx, id); found {
		t.Fatal("re-revoke should report found=false")
	}
	// Unknown id reports not found.
	if found, _ := s.RevokeAdminToken(ctx, "nope"); found {
		t.Fatal("revoke unknown id should report found=false")
	}
	if n, _ := s.CountActiveAdminTokens(ctx); n != 0 {
		t.Fatalf("count after revoke = %d, want 0", n)
	}

	// AdminTokenExists sees revoked rows too.
	if ok, err := s.AdminTokenExists(ctx, id); err != nil || !ok {
		t.Fatalf("exists(revoked) = (%v,%v), want (true,nil)", ok, err)
	}
	if ok, _ := s.AdminTokenExists(ctx, "nope"); ok {
		t.Fatal("exists(unknown) = true, want false")
	}
}
