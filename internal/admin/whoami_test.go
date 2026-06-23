package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWhoamiReturnsEnvAdminRole(t *testing.T) {
	const tok = "env-break-glass-token"
	s := &Server{tokenDigest: digest(tok)}
	req := httptest.NewRequest("GET", "/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
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
	if body.Role != "admin" {
		t.Fatalf("want admin, got %q", body.Role)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Error("whoami must set Cache-Control: no-store")
	}
}

func TestWhoamiRejectsMissingToken(t *testing.T) {
	s := &Server{tokenDigest: digest("x")}
	req := httptest.NewRequest("GET", "/v1/whoami", nil)
	rr := httptest.NewRecorder()
	s.whoami(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatal("missing token must not return 200")
	}
}
