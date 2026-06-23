package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhoamiReturnsPrincipalRole(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/whoami", nil)
	req = req.WithContext(withPrincipal(req.Context(), principal{role: RoleOperator, source: "token"}))
	rr := httptest.NewRecorder()
	s.whoami(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Role != "operator" {
		t.Fatalf("want operator, got %q", body.Role)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Error("whoami must set Cache-Control: no-store")
	}
}

func TestWhoamiFailsClosedWithoutPrincipal(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/v1/whoami", nil) // no principal in context
	rr := httptest.NewRecorder()
	s.whoami(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatal("missing principal must not return 200")
	}
}

// TestWhoamiThroughAuthzEnvAdmin exercises the full authz→whoami path so the
// principal is established by middleware (env break-glass admin).
func TestWhoamiThroughAuthzEnvAdmin(t *testing.T) {
	const tok = "env-break-glass-token"
	s := &Server{tokenDigest: digest(tok)}
	h := s.authz(RoleViewer, s.whoami)
	req := httptest.NewRequest("GET", "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	h(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"role":"admin"`) {
		t.Fatalf("want admin role, got %s", rr.Body.String())
	}
}
