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

func TestReloadSwapsUsersAndRejectsBadRoleTokens(t *testing.T) {
	stub := whoamiStub(t)
	defer stub.Close()
	s := newReloadTestServer(t, stub.URL)
	if err := s.InitialAttest(context.Background()); err != nil {
		t.Fatalf("initial attest: %v", err)
	}
	prev, _ := s.currentRoleTokens()

	// Remove alice; introduce a MISLABELED role-tokens file (admin token on viewer).
	if err := os.WriteFile(s.cfg.UsersFile, []byte("bob:"+mustHash(t, "pw")+":admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := "viewer:" + tokAdmin + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n"
	if err := os.WriteFile(s.cfg.RoleTokensFile, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}

	err := s.Reload(context.Background())
	if err == nil {
		t.Fatal("reload must report the attestation failure")
	}
	// Users swapped (alice gone)...
	if _, ok := s.currentUsers().RoleOf("alice"); ok {
		t.Fatal("alice should be gone after reload")
	}
	if _, ok := s.currentUsers().RoleOf("bob"); !ok {
		t.Fatal("bob should be present after reload")
	}
	// ...but the bad role-token map was NOT published (still the prior snapshot).
	cur, ok := s.currentRoleTokens()
	if !ok || cur != prev {
		t.Fatal("mislabeled role tokens must not be published; prior snapshot must remain")
	}
}
