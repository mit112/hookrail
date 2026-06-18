// internal/admin/server_test.go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
)

func TestOpsRoutesExemptAndV1Guarded(t *testing.T) {
	s := New(nil, nil, [32]byte{}, ssrf.Policy{}, ratelimit.NewRegistry(1, 1), "tok", time.Hour)
	h := s.Handler()

	// health/metrics must NOT require auth (design F17)
	for _, p := range []string{"/healthz", "/metrics"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code == http.StatusUnauthorized {
			t.Fatalf("%s should be exempt from auth, got 401", p)
		}
	}
	// an unauthenticated /v1 route must be 401
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/endpoints", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/endpoints unauth = %d, want 401", w.Code)
	}
}
