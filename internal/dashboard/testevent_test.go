// internal/dashboard/testevent_test.go
package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTestEventForwardsToIngress(t *testing.T) {
	var sawAuth, sawIdem, sawPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth, sawIdem, sawPath = r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, `{"event_id":"ev_1","delivery_ids":[]}`)
	}))
	defer up.Close()
	srv := testServer(t)
	srv.cfg.IngressURL = up.URL
	r := httptest.NewRequest("POST", "/api/test-event", strings.NewReader(`{"topic":"demo.x","payload":{"a":1}}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != 202 { t.Fatalf("want 202, got %d (%s)", w.Code, w.Body) }
	if sawPath != "/v1/events" { t.Errorf("upstream path = %q", sawPath) }
	if sawAuth != "Bearer hk_deadbeef" { t.Errorf("producer key not injected: %q", sawAuth) }
	if sawIdem == "" { t.Error("expected a generated Idempotency-Key") }
}

func TestTestEventRejectsBadBody(t *testing.T) {
	srv := testServer(t)
	r := httptest.NewRequest("POST", "/api/test-event", strings.NewReader(`{"topic":"","payload":1}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(authedCookie(t, srv))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest { t.Fatalf("want 400, got %d", w.Code) }
}
