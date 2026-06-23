//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

func mustJSON(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
}

// reqAs issues a request bearing an arbitrary token (not the env testToken).
func reqAs(t *testing.T, srv *admin.Server, token, method, path string) int {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, r)
	return w.Code
}

func splitMethod(k string) string { // "METHOD /path" -> "METHOD"
	for i := 0; i < len(k); i++ {
		if k[i] == ' ' {
			return k[:i]
		}
	}
	return k
}

func TestRoleMatrix(t *testing.T) {
	srv, s := newServer(t)
	ctx := context.Background()
	tokens := map[admin.Role]string{}
	for _, role := range []string{"viewer", "operator", "admin"} {
		_, plain, err := s.CreateAdminToken(ctx, role, "matrix-"+role)
		if err != nil {
			t.Fatal(err)
		}
		rr, _ := admin.ParseRole(role)
		tokens[rr] = plain
	}
	// One representative path per tier. We only assert the authz decision
	// (403 vs not-403), not the downstream business status.
	//nolint:gosec // G101 false positive: these are URL paths, not credentials.
	probe := map[string]string{
		"GET /v1/deliveries":                "/v1/deliveries",
		"POST /v1/dlq/{delivery_id}/replay": "/v1/dlq/does-not-exist/replay",
		"POST /v1/endpoints":                "/v1/endpoints",
		"POST /v1/admin-tokens":             "/v1/admin-tokens",
	}
	type want struct{ viewer, operator, admin bool } // true = authorized past authz (NOT 403)
	expect := map[string]want{
		"GET /v1/deliveries":                {true, true, true},
		"POST /v1/dlq/{delivery_id}/replay": {false, true, true},
		"POST /v1/endpoints":                {false, false, true},
		"POST /v1/admin-tokens":             {false, false, true},
	}
	check := func(key string, role admin.Role, allowed bool) {
		method, path := splitMethod(key), probe[key]
		code := reqAs(t, srv, tokens[role], method, path)
		if allowed && code == http.StatusForbidden {
			t.Errorf("%s as %v = 403, want authorized", key, role)
		}
		if !allowed && code != http.StatusForbidden {
			t.Errorf("%s as %v = %d, want 403", key, role, code)
		}
	}
	for key, w := range expect {
		check(key, admin.RoleViewer, w.viewer)
		check(key, admin.RoleOperator, w.operator)
		check(key, admin.RoleAdmin, w.admin)
	}
}

func TestAuthFailures(t *testing.T) {
	srv, s := newServer(t)
	// No header -> 401.
	if c := reqAs(t, srv, "", "GET", "/v1/deliveries"); c != http.StatusUnauthorized {
		t.Errorf("no creds = %d, want 401", c)
	}
	// Garbage token -> 401.
	if c := reqAs(t, srv, "hkadm_garbage", "GET", "/v1/deliveries"); c != http.StatusUnauthorized {
		t.Errorf("garbage = %d, want 401", c)
	}
	// Revoked token -> 401.
	id, plain, err := s.CreateAdminToken(context.Background(), "viewer", "to-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeAdminToken(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if c := reqAs(t, srv, plain, "GET", "/v1/deliveries"); c != http.StatusUnauthorized {
		t.Errorf("revoked = %d, want 401", c)
	}
}

func TestBreakGlassEnvToken(t *testing.T) {
	srv, _ := newServer(t)
	// testToken is the env break-glass admin (passed to admin.New in newServer).
	if c := reqAs(t, srv, testToken, "GET", "/v1/deliveries"); c == http.StatusForbidden || c == http.StatusUnauthorized {
		t.Errorf("env token GET = %d, want authorized (admin)", c)
	}
}

func TestDBOutageIs503NotUnauthorized(t *testing.T) {
	srv, s := newServer(t)
	_, plain, err := s.CreateAdminToken(context.Background(), "viewer", "before-outage")
	if err != nil {
		t.Fatal(err)
	}
	s.Close() // force the DB-token lookup to fail with a non-ErrNotFound error
	// A DB-backed token cannot be verified -> 503 from authz (NOT 401).
	if c := reqAs(t, srv, plain, "GET", "/v1/deliveries"); c != http.StatusServiceUnavailable {
		t.Errorf("DB-down lookup = %d, want 503", c)
	}
	// Env break-glass is DB-independent: authz still AUTHORIZES it (not 401/403).
	// The request then 503s in the data handler (DB is down), which is expected
	// and distinct from an authz rejection.
	if c := reqAs(t, srv, testToken, "GET", "/v1/deliveries"); c == http.StatusUnauthorized || c == http.StatusForbidden {
		t.Errorf("env token during outage = %d, want authorized past authz", c)
	}
}

func TestAdminTokenLifecycle(t *testing.T) {
	srv, _ := newServer(t)

	// Create (env admin) -> 201, plaintext once, no-store.
	w := do(t, srv, "POST", "/v1/admin-tokens", map[string]string{"role": "operator", "label": "ci"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201", w.Code)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("create Cache-Control = %q, want no-store", w.Header().Get("Cache-Control"))
	}
	var created struct{ ID, Token, Role string }
	mustJSON(t, w, &created)
	if !strings.HasPrefix(created.Token, "hkadm_") || created.Role != "operator" {
		t.Fatalf("bad create body: %+v", created)
	}

	// The new operator token authorizes replay but not endpoint mutation or minting.
	if c := reqAs(t, srv, created.Token, "POST", "/v1/dlq/x/replay"); c == http.StatusForbidden {
		t.Error("operator token should pass replay authz")
	}
	if c := reqAs(t, srv, created.Token, "POST", "/v1/endpoints"); c != http.StatusForbidden {
		t.Errorf("operator token POST /v1/endpoints = %d, want 403", c)
	}
	if c := reqAs(t, srv, created.Token, "POST", "/v1/admin-tokens"); c != http.StatusForbidden {
		t.Errorf("operator mint = %d, want 403", c)
	}

	// List shows it; never a hash/plaintext; keys match the OpenAPI schema (snake_case).
	lw := do(t, srv, "GET", "/v1/admin-tokens", nil)
	if lw.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", lw.Code)
	}
	if strings.Contains(lw.Body.String(), "token_hash") || strings.Contains(lw.Body.String(), created.Token) {
		t.Error("list leaked hash/plaintext")
	}
	var listed struct {
		Items []map[string]any `json:"items"`
	}
	mustJSON(t, lw, &listed)
	if len(listed.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(listed.Items))
	}
	for _, k := range []string{"id", "role", "label", "created_at"} {
		if _, ok := listed.Items[0][k]; !ok {
			t.Errorf("list item missing OpenAPI key %q; got keys %v", k, listed.Items[0])
		}
	}
	if listed.Items[0]["role"] != "operator" {
		t.Errorf("list item role = %v, want operator", listed.Items[0]["role"])
	}

	// Bad role -> 422.
	if c := do(t, srv, "POST", "/v1/admin-tokens", map[string]string{"role": "root", "label": "x"}).Code; c != http.StatusUnprocessableEntity {
		t.Errorf("bad role = %d, want 422", c)
	}

	// Revoke -> 204; re-revoke -> 204; unknown -> 404; revoked token then 401.
	if c := do(t, srv, "DELETE", "/v1/admin-tokens/"+created.ID, nil).Code; c != http.StatusNoContent {
		t.Errorf("revoke = %d, want 204", c)
	}
	if c := do(t, srv, "DELETE", "/v1/admin-tokens/"+created.ID, nil).Code; c != http.StatusNoContent {
		t.Errorf("re-revoke = %d, want 204", c)
	}
	if c := do(t, srv, "DELETE", "/v1/admin-tokens/nope", nil).Code; c != http.StatusNotFound {
		t.Errorf("revoke unknown = %d, want 404", c)
	}
	if c := reqAs(t, srv, created.Token, "GET", "/v1/deliveries"); c != http.StatusUnauthorized {
		t.Errorf("revoked token = %d, want 401", c)
	}
}

func TestAdminTokenActiveCap(t *testing.T) {
	srv, s := newServer(t)
	ctx := context.Background()
	for i := 0; i < 256; i++ {
		if _, _, err := s.CreateAdminToken(ctx, "viewer", "bulk"); err != nil {
			t.Fatal(err)
		}
	}
	if c := do(t, srv, "POST", "/v1/admin-tokens", map[string]string{"role": "viewer", "label": "over"}).Code; c != http.StatusConflict {
		t.Errorf("257th token = %d, want 409", c)
	}
}
