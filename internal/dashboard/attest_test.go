package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

func mustParseRoleTokens(t *testing.T, src string) *RoleTokens {
	t.Helper()
	rt, err := ParseRoleTokens(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

// whoamiStub maps each configured token to the role it should attest as.
func whoamiStub(t *testing.T) *httptest.Server {
	t.Helper()
	tokenRole := map[string]string{tokViewer: "viewer", tokOperator: "operator", tokAdmin: "admin"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := tokenRole[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
	}))
}

func TestAttestProbe(t *testing.T) {
	srv := whoamiStub(t)
	defer srv.Close()
	if err := attestProbe(context.Background(), srv.URL, mustParseRoleTokens(t, goodRoleTokensFile())); err != nil {
		t.Fatalf("good tokens should attest: %v", err)
	}
	// Mislabeled: admin token on the viewer line.
	bad := "viewer:" + tokAdmin + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n"
	if err := attestProbe(context.Background(), srv.URL, mustParseRoleTokens(t, bad)); err == nil {
		t.Fatal("mislabeled viewer token must fail attestation")
	}
}

func TestAttestStatePublishGetClear(t *testing.T) {
	a := newAttestState()
	if _, ok := a.get(); ok {
		t.Fatal("must start unattested")
	}
	rt := mustParseRoleTokens(t, goodRoleTokensFile())
	a.publish(rt)
	got, ok := a.get()
	if !ok {
		t.Fatal("publish then get must return a snapshot")
	}
	if tok, ok := got.For(admin.RoleAdmin); !ok || tok != tokAdmin {
		t.Fatalf("published snapshot content mismatch: %q", tok)
	}
	a.clear()
	if _, ok := a.get(); ok {
		t.Fatal("clear must drop the snapshot")
	}
}
