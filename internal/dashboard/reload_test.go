package dashboard

import (
	"context"
	"os"
	"testing"
)

// newReloadTestServer builds a Server whose admin URL points at a whoami stub
// and whose users/role-token files live in a temp dir we can rewrite.
func newReloadTestServer(t *testing.T, adminURL string) *Server {
	t.Helper()
	setMinEnv(t)
	dir := t.TempDir()
	uf := dir + "/users"
	tf := dir + "/roletokens"
	if err := os.WriteFile(uf, []byte("alice:"+mustHash(t, "pw")+":admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tf, []byte(goodRoleTokensFile()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKRAIL_DASHBOARD_USERS_FILE", uf)
	t.Setenv("HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE", tf)
	t.Setenv("HOOKRAIL_ADMIN_URL", adminURL)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(cfg)
}

func TestInitialAttestPublishes(t *testing.T) {
	stub := whoamiStub(t)
	defer stub.Close()
	s := newReloadTestServer(t, stub.URL)
	if err := s.InitialAttest(context.Background()); err != nil {
		t.Fatalf("initial attest: %v", err)
	}
	if _, ok := s.currentRoleTokens(); !ok {
		t.Fatal("attested snapshot must be active after InitialAttest")
	}
}

func TestReloadSwapsUsersAndFailsClosedOnMislabeledTokens(t *testing.T) {
	stub := whoamiStub(t)
	defer stub.Close()
	s := newReloadTestServer(t, stub.URL)
	if err := s.InitialAttest(context.Background()); err != nil {
		t.Fatalf("initial attest: %v", err)
	}
	if _, ok := s.currentRoleTokens(); !ok {
		t.Fatal("must be attested after initial")
	}

	// Remove alice; introduce a MISLABELED role-tokens file (admin token on viewer).
	if err := os.WriteFile(s.cfg.UsersFile, []byte("bob:"+mustHash(t, "pw")+":admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := "viewer:" + tokAdmin + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n"
	if err := os.WriteFile(s.cfg.RoleTokensFile, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.Reload(context.Background()); err == nil {
		t.Fatal("reload must report the attestation failure")
	}
	// Users swapped...
	if _, ok := s.currentUsers().RoleOf("alice"); ok {
		t.Fatal("alice should be gone after reload")
	}
	if _, ok := s.currentUsers().RoleOf("bob"); !ok {
		t.Fatal("bob should be present after reload")
	}
	// ...and the mislabeled tokens fail closed (no active snapshot → 503).
	if _, ok := s.currentRoleTokens(); ok {
		t.Fatal("mislabeled role tokens must fail closed, not keep serving")
	}
}

func TestReloadKeepsOldSnapshotOnParseError(t *testing.T) {
	stub := whoamiStub(t)
	defer stub.Close()
	s := newReloadTestServer(t, stub.URL)
	if err := s.InitialAttest(context.Background()); err != nil {
		t.Fatalf("initial attest: %v", err)
	}
	prev, _ := s.currentRoleTokens()

	// A malformed (unparseable) role-tokens file must NOT touch desired/current.
	if err := os.WriteFile(s.cfg.RoleTokensFile, []byte("garbage-not-role-tokens\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(context.Background()); err == nil {
		t.Fatal("reload must report the parse error")
	}
	cur, ok := s.currentRoleTokens()
	if !ok || cur != prev {
		t.Fatal("a parse error must keep the previously attested snapshot serving")
	}
}
