package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	keyFile := setMinEnv(t)
	_ = keyFile
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(cfg)
}

func TestLoginWrongPassword(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"nope"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 {
		t.Fatal("no cookie on failed login")
	}
}

func TestLoginRightPasswordSetsCookie(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"s3cret-long-enough"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
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
		t.Error("cookie must be HttpOnly+SameSite=Strict")
	}
}

func TestThrottleLocksOut(t *testing.T) {
	srv := testServer(t)
	for i := 0; i < 12; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"nope"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "10.0.0.9:1234"
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		if i >= 10 && w.Code != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: want 429, got %d", i, w.Code)
		}
	}
}

func TestLoginOversizedBody_Returns413(t *testing.T) {
	srv := testServer(t)
	// Build valid JSON body just over 64 KiB — the only reason to reject is size
	password := strings.Repeat("a", 64*1024-13) // 65523 a's = 65537 total body len
	body := `{"password":"` + password + `"}`
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", w.Code)
	}
}
