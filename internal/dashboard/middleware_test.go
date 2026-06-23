package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

// Test helper only — no dynamic Secure flag needed.
//nolint:gosec
func authedCookie(t *testing.T, s *Server) *http.Cookie {
	return &http.Cookie{Name: s.cookieName(), Value: s.sessions.Issue(s.now(), "alice")}
}

// subjectCookie issues a session cookie for an arbitrary subject (test helper).
//
//nolint:gosec
func subjectCookie(s *Server, sub string) *http.Cookie {
	return &http.Cookie{Name: s.cookieName(), Value: s.sessions.Issue(s.now(), sub)}
}

func TestRequireRoleNoCookie(t *testing.T) {
	srv := testServer(t)
	h := srv.requireRole(admin.RoleViewer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest("GET", "/v1/endpoints", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestRequireRoleGoodCookieViewer(t *testing.T) {
	srv := testServer(t) // alice is admin
	h := srv.requireRole(admin.RoleViewer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestRequireRoleInsufficientRole(t *testing.T) {
	srv := testServerWithUsers(t, "viewerbob:"+mustHash(t, "pw")+":viewer\n")
	h := srv.requireRole(admin.RoleAdmin, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(subjectCookie(srv, "viewerbob"))
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer→admin: want 403, got %d", w.Code)
	}
}

func TestRequireRoleRejectsDeletedUser(t *testing.T) {
	srv := testServer(t)
	cookie := authedCookie(t, srv) // subject "alice"
	u, err := ParseUsers(strings.NewReader("bob:" + mustHash(t, "x") + ":admin\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv.usersPtr.Store(u)
	h := srv.requireRole(admin.RoleViewer, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("GET", "/v1/endpoints", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user: want 401, got %d", w.Code)
	}
}

func TestRequireRoleRejectsFormPost(t *testing.T) {
	srv := testServer(t)
	h := srv.requireRole(admin.RoleAdmin, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r := httptest.NewRequest("POST", "/v1/endpoints", nil)
	r.AddCookie(authedCookie(t, srv))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", w.Code)
	}
}
