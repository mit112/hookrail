// internal/dashboard/server_handler_test.go
package dashboard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthzOpen(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 { t.Fatalf("healthz want 200, got %d", w.Code) }
}

func TestSPAFallbackServesIndex(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/endpoints", nil)) // a client-side route
	if w.Code != 200 { t.Fatalf("SPA fallback want 200, got %d", w.Code) }
}

func TestV1RequiresAuth(t *testing.T) {
	srv := testServer(t)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/endpoints", nil)) // no cookie
	if w.Code != http.StatusUnauthorized { t.Fatalf("want 401, got %d", w.Code) }
}
