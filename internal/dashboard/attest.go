package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mit112/hookrail/internal/admin"
)

// attestState holds the active, attested role-token snapshot. current is nil
// until the first successful attestation and is cleared when a re-probe fails;
// proxyAdmin fails closed (503) while nil. last keeps the most recent intended
// snapshot so the periodic re-probe can re-attest and recover after a transient
// upstream failure.
type attestState struct {
	current atomic.Pointer[RoleTokens]
	last    atomic.Pointer[RoleTokens]
}

func newAttestState() *attestState { return &attestState{} }

func (a *attestState) get() (*RoleTokens, bool) {
	rt := a.current.Load()
	return rt, rt != nil
}

// publish stores an independent clone (immutable, decoupled from any caller
// reference) as both the active and the last-intended snapshot.
func (a *attestState) publish(rt *RoleTokens) {
	c := rt.clone()
	a.last.Store(c)
	a.current.Store(c)
}

// clear drops the active snapshot (fail closed) but keeps last for re-probe.
func (a *attestState) clear() { a.current.Store(nil) }

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

// StartReprobe periodically re-attests the last-intended snapshot. On failure it
// clears the active snapshot (proxyAdmin/readyz fail closed); on later success it
// republishes (recovery). Continuous enforcement of the D11 contract.
func (s *Server) StartReprobe(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				src := s.attest.last.Load()
				if src == nil {
					continue // nothing has ever attested
				}
				if err := attestProbe(ctx, s.cfg.AdminURL, src); err != nil {
					s.attest.clear()
					s.log.Error("attestation re-probe failed; role tokens cleared", "err", err)
				} else if _, ok := s.attest.get(); !ok {
					// src is already a private clone; republish directly.
					s.attest.current.Store(src)
					s.log.Info("attestation re-probe recovered; role tokens republished")
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
