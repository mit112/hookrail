package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mit112/hookrail/internal/admin"
)

// attestState holds the active, attested role-token snapshot. current is nil
// until the first successful attestation and is cleared when a re-probe fails;
// proxyAdmin fails closed (503) while nil. last keeps the most recent intended
// snapshot so the periodic re-probe can re-attest and recover. A monotonic gen
// counter lets the re-probe discard results derived from a snapshot that a
// concurrent reload has since superseded (no stale clobber/rollback).
type attestState struct {
	mu      sync.Mutex
	current *RoleTokens
	last    *RoleTokens
	gen     uint64
}

func newAttestState() *attestState { return &attestState{} }

func (a *attestState) get() (*RoleTokens, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current, a.current != nil
}

// publish stores an independent clone (immutable, decoupled from any caller
// reference) as both the active and the last-intended snapshot, bumping gen.
func (a *attestState) publish(rt *RoleTokens) {
	c := rt.clone()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = c
	a.current = c
	a.gen++
}

// reprobeSource returns the snapshot to re-probe and the gen it was read at.
func (a *attestState) reprobeSource() (*RoleTokens, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last, a.gen
}

// applyReprobe applies a re-probe outcome iff no publish/clear happened since
// genBefore (otherwise the result is stale and dropped). On failure it clears
// the active snapshot; on success it republishes a clone of src if currently
// cleared. Returns the bump so callers can observe whether it applied.
func (a *attestState) applyReprobe(genBefore uint64, src *RoleTokens, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gen != genBefore {
		return // a concurrent reload superseded this probe
	}
	if !ok {
		if a.current != nil {
			a.current = nil
			a.gen++
		}
		return
	}
	if a.current == nil {
		c := src.clone()
		a.current = c
		a.last = c
		a.gen++
	}
}

// currentRoleTokens returns the active attested snapshot, if any.
func (s *Server) currentRoleTokens() (*RoleTokens, bool) { return s.attest.get() }

// attestAndPublish probes rt and, only on success, makes it the active snapshot.
func (s *Server) attestAndPublish(ctx context.Context, rt *RoleTokens) error {
	if err := attestProbe(ctx, s.cfg.AdminURL, rt); err != nil {
		return err
	}
	s.attest.publish(rt)
	return nil
}

// StartReprobe periodically re-attests the intended snapshot. On failure it
// clears the active snapshot (proxyAdmin/readyz fail closed); on success it
// republishes if cleared (recovery). It also recovers from a cold-start failure
// where nothing ever published by falling back to the configured startup tokens.
// A gen check ensures a probe result is dropped if a reload superseded it.
func (s *Server) StartReprobe(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				src, gen := s.attest.reprobeSource()
				if src == nil {
					src = s.cfg.RoleTokens // cold-start fallback: keep trying startup tokens
				}
				err := attestProbe(ctx, s.cfg.AdminURL, src)
				s.attest.applyReprobe(gen, src, err == nil)
				if err != nil {
					s.log.Error("attestation re-probe failed; role tokens cleared", "err", err)
				}
			}
		}
	}()
}

// attestProbe confirms each configured token's /v1/whoami role equals its
// declared role. Any mismatch or probe error fails the whole attestation.
func attestProbe(ctx context.Context, adminURL string, rt *RoleTokens) error {
	for _, role := range rt.Roles() {
		tok, _ := rt.For(role)
		got, err := whoamiRole(ctx, adminURL, tok)
		if err != nil {
			return fmt.Errorf("attest %s: %w", role, err)
		}
		if got != role {
			return fmt.Errorf("attest %s: token resolves to %s (mislabeled)", role, got)
		}
	}
	return nil
}

func whoamiRole(ctx context.Context, adminURL, token string) (admin.Role, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// adminURL is an env-configured upstream, by design.
	//nolint:gosec
	req, err := http.NewRequestWithContext(ctx, "GET", adminURL+"/v1/whoami", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := proxyClient.Do(req)
	if err != nil {
		return 0, err
	}
	// Body closed by defer — close error is harmless.
	//nolint:errcheck
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("whoami status %d", resp.StatusCode)
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<10)).Decode(&body); err != nil {
		return 0, err
	}
	role, ok := admin.ParseRole(body.Role)
	if !ok {
		return 0, fmt.Errorf("whoami returned invalid role %q", body.Role)
	}
	return role, nil
}
