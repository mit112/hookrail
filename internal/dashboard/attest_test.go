package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestAttestStatePublishGetApplyReprobe(t *testing.T) {
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

	// A failed re-probe at the current gen clears the active snapshot.
	src, gen := a.reprobeSource()
	a.applyReprobe(gen, src, false)
	if _, ok := a.get(); ok {
		t.Fatal("failed re-probe must clear the snapshot")
	}
	// A successful re-probe republishes.
	src, gen = a.reprobeSource()
	a.applyReprobe(gen, src, true)
	if _, ok := a.get(); !ok {
		t.Fatal("successful re-probe must republish")
	}

	// A stale re-probe (wrong gen) is dropped: a newer publish must survive a
	// late failing probe carrying an old gen.
	_, staleGen := a.reprobeSource()
	a.publish(rt) // bumps gen
	a.applyReprobe(staleGen, src, false)
	if _, ok := a.get(); !ok {
		t.Fatal("stale re-probe result must not clear a newer snapshot")
	}
}

func TestAttestConcurrentReloadVsReprobe(t *testing.T) {
	a := newAttestState()
	rt := mustParseRoleTokens(t, goodRoleTokensFile())
	a.publish(rt)
	var wg sync.WaitGroup
	// Simulated SIGHUP reloads.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			a.publish(rt)
		}
	}()
	// Simulated re-probe ticks (both success and failure outcomes).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			src, gen := a.reprobeSource()
			if src == nil {
				src = rt
			}
			a.applyReprobe(gen, src, i%2 == 0)
		}
	}()
	// Concurrent readers (proxy hot path).
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = a.get()
		}
	}()
	wg.Wait()
	// Final reload wins and leaves a consistent attested snapshot.
	a.publish(rt)
	if _, ok := a.get(); !ok {
		t.Fatal("expected an attested snapshot after a final publish")
	}
}

func TestReprobeRecoversFromColdStartFailure(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
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
	// Cold start: initial attestation fails, nothing ever published.
	if err := s.InitialAttest(context.Background()); err == nil {
		t.Fatal("initial attest should fail while upstream is down")
	}
	if _, ok := s.currentRoleTokens(); ok {
		t.Fatal("must be unattested after cold-start failure")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartReprobe(ctx, 5*time.Millisecond)
	// Bring upstream up; the re-probe must recover with no operator action.
	failing.Store(false)
	waitFor(t, 2*time.Second, func() bool { _, ok := s.currentRoleTokens(); return ok })
}
