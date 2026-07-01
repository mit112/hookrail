package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// demoUsers builds a users file with a read-only demo user plus an admin.
func demoUsers(t *testing.T) string {
	t.Helper()
	return "demo:" + mustHash(t, "pw-demo") + ":viewer\n" +
		"boss:" + mustHash(t, "pw-boss") + ":admin\n"
}

func TestPublicConfigReflectsDemoFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		on   bool
		want string
	}{
		{"off", false, `"demo":false`},
		{"on", true, `"demo":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.on {
				t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
				t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "demo")
			}
			srv := testServerWithUsers(t, demoUsers(t))
			r := httptest.NewRequest("GET", "/api/public-config", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("want 200, got %d", w.Code)
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("want body to contain %s, got %s", tc.want, w.Body.String())
			}
		})
	}
}

func TestDemoLoginDisabledReturns404(t *testing.T) {
	srv := testServerWithUsers(t, demoUsers(t)) // demo mode NOT set
	r := httptest.NewRequest("POST", "/api/demo-login", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("demo disabled: want 404, got %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("no cookie when demo mode is off")
	}
}

func TestDemoLoginIssuesViewerSession(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "demo")
	srv := testServerWithUsers(t, demoUsers(t))

	r := httptest.NewRequest("POST", "/api/demo-login", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "hk_dash" {
			found = c
		}
	}
	if found == nil {
		t.Fatal("expected hk_dash cookie")
	}
	if !found.HttpOnly || found.SameSite != http.SameSiteStrictMode {
		t.Error("demo cookie must be HttpOnly + SameSite=Strict")
	}
	// The issued session must resolve to viewer, never higher.
	sr := httptest.NewRequest("GET", "/api/session", nil)
	sr.AddCookie(found)
	sw := httptest.NewRecorder()
	srv.Handler().ServeHTTP(sw, sr)
	if sw.Code != http.StatusOK || !strings.Contains(sw.Body.String(), `"role":"viewer"`) {
		t.Fatalf("session: code=%d body=%s", sw.Code, sw.Body.String())
	}
}

// Defense in depth: even if the demo user were somehow pointed at a privileged
// account at runtime, the handler must refuse to issue a session.
func TestDemoLoginRefusesNonViewer(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "demo")
	srv := testServerWithUsers(t, demoUsers(t))
	// Simulate a misconfiguration that bypassed load-time validation.
	srv.cfg.DemoUser = "boss" // admin in the users file

	r := httptest.NewRequest("POST", "/api/demo-login", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-viewer demo user: want 403, got %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("must not issue a cookie for a non-viewer demo user")
	}
}

func TestConfigDemoModeRejectsNonViewerUser(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "boss") // admin
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	// testServerWithUsers calls LoadConfig and t.Fatals on error; build manually.
	setMinEnv(t)
	dir := t.TempDir()
	uf := dir + "/users"
	if err := os.WriteFile(uf, []byte(demoUsers(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKRAIL_DASHBOARD_USERS_FILE", uf)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to reject a non-viewer demo user")
	}
}

// The public demo invariant must hold across a hot reload: a users file that
// promotes the demo user above viewer must be refused, keeping the prior state.
func TestReloadRefusesDemoUserPromotion(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "demo")
	setMinEnv(t)
	dir := t.TempDir()
	uf := dir + "/users"
	if err := os.WriteFile(uf, []byte(demoUsers(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKRAIL_DASHBOARD_USERS_FILE", uf)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg)

	// Attacker/operator edits the file to promote the public demo user.
	if err := os.WriteFile(uf, []byte("demo:"+mustHash(t, "x")+":admin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.Reload(context.Background()); err == nil {
		t.Fatal("expected Reload to refuse promoting the demo user above viewer")
	}
	if role, ok := srv.currentUsers().RoleOf("demo"); !ok || role.String() != "viewer" {
		t.Fatalf("demo user must remain viewer after refused reload, got %v ok=%v", role, ok)
	}
}

func TestConfigDemoModeRejectsMissingUser(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "ghost")
	setMinEnv(t)
	dir := t.TempDir()
	uf := dir + "/users"
	if err := os.WriteFile(uf, []byte(demoUsers(t)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOOKRAIL_DASHBOARD_USERS_FILE", uf)
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig to reject a missing demo user")
	}
}
