//go:build integration

package admin_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEveryV1RouteRequiresToken(t *testing.T) {
	srv, _ := newServer(t)
	h := srv.Handler()
	routes := []struct{ method, path string }{
		{"POST", "/v1/endpoints"}, {"GET", "/v1/endpoints"}, {"GET", "/v1/endpoints/x"},
		{"PATCH", "/v1/endpoints/x"}, {"DELETE", "/v1/endpoints/x"}, {"POST", "/v1/endpoints/x/rotate-secret"},
		{"POST", "/v1/subscriptions"}, {"GET", "/v1/subscriptions"}, {"GET", "/v1/subscriptions/x"},
		{"PATCH", "/v1/subscriptions/x"}, {"DELETE", "/v1/subscriptions/x"},
		{"GET", "/v1/dlq"}, {"POST", "/v1/dlq/x/replay"}, {"GET", "/v1/deliveries"}, {"GET", "/v1/deliveries/x"},
	}
	for _, rt := range routes {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(rt.method, rt.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d, want 401", rt.method, rt.path, w.Code)
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
