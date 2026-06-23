package dashboard

import (
	"os"
	"path/filepath"
	"testing"
)

func setMinEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "pk")
	if err := os.WriteFile(keyFile, []byte("hk_deadbeef"), 0o600); err != nil { t.Fatal(err) }
	usersFile := filepath.Join(dir, "users")
	if err := os.WriteFile(usersFile, []byte("alice:"+mustHash(t, "pw-alice")+":admin\n"), 0o600); err != nil { t.Fatal(err) }
	tokensFile := filepath.Join(dir, "roletokens")
	if err := os.WriteFile(tokensFile, []byte(goodRoleTokensFile()), 0o600); err != nil { t.Fatal(err) }
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("HOOKRAIL_PRODUCER_KEY_FILE", keyFile)
	t.Setenv("HOOKRAIL_ADMIN_URL", "http://admin:8082")
	t.Setenv("HOOKRAIL_INGRESS_URL", "http://api:8080")
	t.Setenv("HOOKRAIL_DASHBOARD_USERS_FILE", usersFile)
	t.Setenv("HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE", tokensFile)
	return keyFile
}

func TestLoadConfigOK(t *testing.T) {
	setMinEnv(t)
	cfg, err := LoadConfig()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if cfg.ProducerKey != "hk_deadbeef" { t.Errorf("producer key not read from file: %q", cfg.ProducerKey) }
	if cfg.Addr != ":8085" { t.Errorf("default addr wrong: %q", cfg.Addr) }
	if cfg.Users == nil || cfg.RoleTokens == nil { t.Fatal("Users and RoleTokens must be populated") }
}

func TestLoadConfigRequiresUserAndTokenFiles(t *testing.T) {
	setMinEnv(t)
	os.Unsetenv("HOOKRAIL_DASHBOARD_USERS_FILE") //nolint:errcheck
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error without users file") }
	setMinEnv(t)
	os.Unsetenv("HOOKRAIL_DASHBOARD_ROLE_TOKENS_FILE") //nolint:errcheck
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error without role-tokens file") }
}

func TestLoadConfigMissingSessionKeyTooShort(t *testing.T) {
	setMinEnv(t)
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "tooshort")
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error for short session key") }
}

