package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Test helper only — no dynamic Secure flag needed.
//nolint:gosec
func authedCookie(t *testing.T, s *Server) *http.Cookie {
	return &http.Cookie{Name: s.cookieName(), Value: s.sessions.Issue(s.now(), "alice")}
}

func TestRequireSessionNoCookie(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/v1/endpoints", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestRequireSessionGoodCookie(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestRequireSessionRejectsFormPost(t *testing.T) {
	srv := testServer(t)
	h := srv.requireSession(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("POST", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", w.Code)
	}
}
