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
	t.Setenv("HOOKRAIL_DASHBOARD_PASSWORD", "s3cret-long-enough")
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("HOOKRAIL_ADMIN_TOKEN", "dev-admin-token-001")
	t.Setenv("HOOKRAIL_PRODUCER_KEY_FILE", keyFile)
	t.Setenv("HOOKRAIL_ADMIN_URL", "http://admin:8082")
	t.Setenv("HOOKRAIL_INGRESS_URL", "http://api:8080")
	return keyFile
}

func TestLoadConfigOK(t *testing.T) {
	setMinEnv(t)
	cfg, err := LoadConfig()
	if err != nil { t.Fatalf("unexpected error: %v", err) }
	if cfg.ProducerKey != "hk_deadbeef" { t.Errorf("producer key not read from file: %q", cfg.ProducerKey) }
	if cfg.Addr != ":8085" { t.Errorf("default addr wrong: %q", cfg.Addr) }
}

func TestLoadConfigMissingSessionKeyTooShort(t *testing.T) {
	setMinEnv(t)
	t.Setenv("HOOKRAIL_DASHBOARD_SESSION_KEY", "tooshort")
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error for short session key") }
}

func TestLoadConfigMissingPassword(t *testing.T) {
	setMinEnv(t)
	// Literal string — can't fail.
	os.Unsetenv("HOOKRAIL_DASHBOARD_PASSWORD") //nolint:errcheck
	if _, err := LoadConfig(); err == nil { t.Fatal("expected error for missing password") }
}

func TestLoadConfig_RejectsShortPassword(t *testing.T) {
	setMinEnv(t)
	t.Setenv("HOOKRAIL_DASHBOARD_PASSWORD", "short")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("want error for <16-char password")
	}
}
