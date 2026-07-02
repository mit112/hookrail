package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/admin"
)

// Regression: behind a TLS-terminating proxy (Funnel/Cloudflare) r.TLS is nil,
// so the browser's https Origin must still be accepted against the http host.
// Previously a scheme-sensitive check 403'd every same-origin mutating request
// (e.g. POST /api/logout) on the live HTTPS demo.
func TestSameOriginIgnoresScheme(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"no origin header", "dash.example.com", "", true},
		{"same host, https origin over http backend", "dash.example.com", "https://dash.example.com", true},
		{"same host, same scheme", "dash.example.com", "http://dash.example.com", true},
		{"https origin default port vs bare backend host", "dash.example.com", "https://dash.example.com:443", true},
		{"backend default port, origin bare", "dash.example.com:443", "https://dash.example.com", true},
		{"local dev direct, matching non-default port", "localhost:8085", "http://localhost:8085", true},
		{"uppercase origin host", "dash.example.com", "https://DASH.EXAMPLE.COM", true},
		{"trailing dot origin", "dash.example.com", "https://dash.example.com.", true},
		{"non-default origin port vs bare host", "dash.example.com", "https://dash.example.com:8080", false},
		{"mismatched non-default ports", "dash.example.com:8085", "http://dash.example.com:9090", false},
		{"different host", "dash.example.com", "https://evil.example.com", false},
		{"null origin", "dash.example.com", "null", false},
		{"garbage origin", "dash.example.com", "://not a url", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/endpoints", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Fatalf("sameOrigin=%v want %v (host=%q origin=%q)", got, tc.want, tc.host, tc.origin)
			}
		})
	}
}

// A cross-scheme same-host mutating request must pass requireRole's CSRF guard.
func TestRequireRoleAllowsCrossSchemeSameOrigin(t *testing.T) {
	srv := testServer(t) // alice is admin
	h := srv.requireRole(admin.RoleAdmin, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("POST", "/v1/endpoints", nil)
	r.Host = "dash.example.com"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://dash.example.com") // https from browser, http backend
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 204 {
		t.Fatalf("cross-scheme same-origin: want 204, got %d", w.Code)
	}
}

func TestLoginDisabledInDemoMode(t *testing.T) {
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_MODE", "true")
	t.Setenv("HOOKRAIL_DASHBOARD_DEMO_USER", "demo")
	srv := testServerWithUsers(t, "demo:"+mustHash(t, "pw")+":viewer\n")
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("demo mode login: want 404, got %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("demo-mode login must not issue a cookie")
	}
}

// The per-session throttle caps a single session and, crucially, does NOT
// starve a different session — a hostile visitor looping on one cookie must not
// 429 everyone else (the Codex pre-gate HIGH). Unauthenticated requests are
// rejected before any bucket is touched.
func TestPerSessionThrottleIsIsolated(t *testing.T) {
	srv := testServerWithUsers(t, "alice:"+mustHash(t, "x")+":admin\nbob:"+mustHash(t, "y")+":viewer\n")
	srv.sessThr = newThrottle(2, time.Minute)
	h := srv.requireRole(admin.RoleViewer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	call := func(c *http.Cookie) int {
		r := httptest.NewRequest("GET", "/v1/endpoints", nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	alice := subjectCookie(srv, "alice")
	bob := subjectCookie(srv, "bob")

	// Anonymous request: 401, and must not consume a bucket.
	if c := call(nil); c != http.StatusUnauthorized {
		t.Fatalf("anonymous: want 401, got %d", c)
	}
	// Alice burns her per-session bucket.
	if c := call(alice); c != 200 {
		t.Fatalf("alice req 1: want 200, got %d", c)
	}
	if c := call(alice); c != 200 {
		t.Fatalf("alice req 2: want 200, got %d", c)
	}
	if c := call(alice); c != http.StatusTooManyRequests {
		t.Fatalf("alice req 3: want 429, got %d", c)
	}
	// Bob's separate session is unaffected — no cross-session starvation.
	if c := call(bob); c != 200 {
		t.Fatalf("bob (isolated session): want 200, got %d", c)
	}
}

// The global backstop protects the datastore across all sessions.
func TestGlobalBackstopCapsAllSessions(t *testing.T) {
	srv := testServerWithUsers(t, "alice:"+mustHash(t, "x")+":admin\nbob:"+mustHash(t, "y")+":viewer\n")
	srv.sessThr = newThrottle(100, time.Minute) // not the limiting factor here
	srv.apiThr = newThrottle(2, time.Minute)    // global ceiling
	h := srv.requireRole(admin.RoleViewer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	call := func(sub string) int {
		r := httptest.NewRequest("GET", "/v1/endpoints", nil)
		r.AddCookie(subjectCookie(srv, sub))
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	if c := call("alice"); c != 200 {
		t.Fatalf("global req 1: want 200, got %d", c)
	}
	if c := call("bob"); c != 200 {
		t.Fatalf("global req 2: want 200, got %d", c)
	}
	// Third request from a fresh session still hits the shared global ceiling.
	if c := call("alice"); c != http.StatusTooManyRequests {
		t.Fatalf("global req 3: want 429, got %d", c)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	for _, hdr := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if w.Header().Get(hdr) == "" {
			t.Errorf("missing security header %q", hdr)
		}
	}
	if w.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be set on a plain-HTTP request")
	}
}

func TestHSTSSetWhenForwardedHTTPS(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS should be set when X-Forwarded-Proto=https")
	}
}
