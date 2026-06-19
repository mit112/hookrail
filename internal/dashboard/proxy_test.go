package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyInjectsTokenStripsSmuggled(t *testing.T) {
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

	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	r := httptest.NewRequest("GET", "/v1/endpoints?limit=5", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Authorization", "Bearer smuggled")
	r.Header.Set("Proxy-Authorization", "Bearer smuggled2")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)

	if w.Code != 200 { t.Fatalf("want 200, got %d", w.Code) }
	if sawAuth != "Bearer admintok" { t.Errorf("upstream Authorization = %q, want injected admin token", sawAuth) }
	if sawCookie != "" { t.Errorf("cookie leaked upstream: %q", sawCookie) }
	if sawProxyAuth != "" { t.Errorf("proxy-authorization leaked upstream: %q", sawProxyAuth) }
}

func TestProxyUnknownPath404(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("GET", "/v1/secret-backdoor", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound { t.Fatalf("unknown path must be 404, got %d", w.Code) }
}

func TestProxyDoesNotFollowRedirect(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://evil.example/", http.StatusFound)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusFound { t.Fatalf("redirect must pass through (302), got %d", w.Code) }
}

func TestProxyRejectsOversizeBody(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.AdminURL = up.URL
	big := strings.Repeat("a", (256<<10)+1)
	r := httptest.NewRequest("POST", "/v1/endpoints", strings.NewReader(big))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge { t.Fatalf("want 413, got %d", w.Code) }
}
