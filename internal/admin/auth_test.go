// internal/admin/auth_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAuthOnly(token string) *Server {
	return &Server{tokenDigest: digest(token)}
}

// TestAuthzEnvBreakGlass exercises the env break-glass path through authz()
// for cases that never touch the store (valid env token, missing, empty). The
// DB-token failure cases (garbage/revoked → 401, low role → 403, DB error →
// 503) are covered by the integration suite in authz_integration_test.go.
func TestAuthzEnvBreakGlass(t *testing.T) {
	s := newAuthOnly("s3cret-token")
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	h := s.authz(RoleAdmin, ok)

	cases := []struct {
		name, header string
		want         int
	}{
		{"valid env token = admin", "Bearer s3cret-token", 200},
		{"missing", "", 401},
		{"empty bearer", "Bearer ", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/endpoints", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != c.want {
				t.Fatalf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}
