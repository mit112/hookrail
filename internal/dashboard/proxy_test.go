package dashboard

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

// publishTestTokens makes a valid attested snapshot active so proxyAdmin serves.
func publishTestTokens(t *testing.T, srv *Server) {
	t.Helper()
	srv.attest.publish(mustParseRoleTokens(t, goodRoleTokensFile()))
}

func TestProxyInjectsRoleMatchedTokenStripsSmuggled(t *testing.T) {
	var sawAuth, sawCookie, sawProxyAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawCookie = r.Header.Get("Cookie")
		sawProxyAuth = r.Header.Get("Proxy-Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"items":[],"next_cursor":""}`)
	}))
	defer up.Close()

	srv := testServer(t) // alice is admin
	srv.cfg.AdminURL = up.URL
	publishTestTokens(t, srv)
	r := httptest.NewRequest("GET", "/v1/endpoints?limit=5", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Authorization", "Bearer smuggled")
	r.Header.Set("Proxy-Authorization", "Bearer smuggled2")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if sawAuth != "Bearer "+tokAdmin {
		t.Errorf("upstream Authorization = %q, want admin role token", sawAuth)
	}
	if sawCookie != "" {
		t.Errorf("cookie leaked upstream: %q", sawCookie)
	}
	if sawProxyAuth != "" {
		t.Errorf("proxy-authorization leaked upstream: %q", sawProxyAuth)
	}
}

func TestProxyForwardsViewerTokenForViewerSession(t *testing.T) {
	var sawAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	}))
	defer up.Close()
	srv := testServerWithUsers(t, "carol:"+mustHash(t, "pw")+":viewer\n")
	srv.cfg.AdminURL = up.URL
	publishTestTokens(t, srv)
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(subjectCookie(srv, "carol"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if sawAuth != "Bearer "+tokViewer {
		t.Fatalf("viewer session should forward viewer token, got %q", sawAuth)
	}
}

func TestProxy503WhenUnattested(t *testing.T) {
	srv := testServer(t) // no snapshot published
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unattested proxy: want 503, got %d", w.Code)
	}
}

// proxyAdmin direct-call 503 (covers the gate independent of routing).
func TestProxyAdminDirect503WhenUnattested(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest("GET", "/v1/endpoints", nil).
		WithContext(withRole(context.Background(), "op", admin.RoleOperator))
	w := httptest.NewRecorder()
	srv.proxyAdmin(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", w.Code)
	}
}

func TestProxyUnknownPath404(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("GET", "/v1/secret-backdoor", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown path must be 404, got %d", w.Code)
	}
}

func TestProxyDoesNotFollowRedirect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/", http.StatusFound)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	publishTestTokens(t, srv)
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("redirect must pass through (302), got %d", w.Code)
	}
}

func TestProxyRejectsOversizeBody(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	publishTestTokens(t, srv)
	big := strings.Repeat("a", (256<<10)+1)
	r := httptest.NewRequest("POST", "/v1/endpoints", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", w.Code)
	}
}
