//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// concretePath turns "/v1/x/{id}/y" into "/v1/x/PARAM/y".
func concretePath(pattern string) string {
	out := pattern
	for {
		i := strings.IndexByte(out, '{')
		if i < 0 {
			return out
		}
		j := strings.IndexByte(out, '}')
		out = out[:i] + "PARAM" + out[j+1:]
	}
}

// TestEveryV1RouteRequiresToken iterates the single route registry so it can
// never drift from the routes the server actually registers (it now also
// covers skip and ordered-keys, which the old hand-maintained list omitted).
func TestEveryV1RouteRequiresToken(t *testing.T) {
	srv, _ := newServer(t)
	h := srv.Handler()
	for _, rt := range srv.RouteTable() {
		path := concretePath(rt.Pattern)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(rt.Method, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", rt.Method, path, w.Code)
		}
	}
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code == http.StatusUnauthorized {
			t.Errorf("ops route %s required auth", p)
		}
	}
}
