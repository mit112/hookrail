package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func TestReprobeClearsThenRecovers(t *testing.T) {
	var failing atomic.Bool
	tokenRole := map[string]string{tokViewer: "viewer", tokOperator: "operator", tokAdmin: "admin"}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		role, ok := tokenRole[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"role": role})
	}))
	defer stub.Close()

	s := newReloadTestServer(t, stub.URL)
	if err := s.InitialAttest(context.Background()); err != nil {
		t.Fatalf("initial attest: %v", err)
	}
	if _, ok := s.currentRoleTokens(); !ok {
		t.Fatal("must be attested initially")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartReprobe(ctx, 5*time.Millisecond)

	// Break the upstream → the active snapshot must be cleared (fail closed).
	failing.Store(true)
	waitFor(t, 2*time.Second, func() bool { _, ok := s.currentRoleTokens(); return !ok })

	// Restore → the re-probe must recover and republish.
	failing.Store(false)
	waitFor(t, 2*time.Second, func() bool { _, ok := s.currentRoleTokens(); return ok })
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
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
