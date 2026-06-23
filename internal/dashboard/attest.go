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

// attestState tracks the DESIRED role-token set (the latest loaded from disk)
// and the CURRENT active snapshot (attested; nil = proxyAdmin/readyz fail
// closed). The re-probe loop drives current toward desired: a successful probe
// of desired makes it active; a failure clears current. desired is updated on
// every load (InitialAttest/Reload) even when attestation fails, so a rotation
// during a transient upstream outage still recovers to the NEW tokens — never
// wedges on stale ones. gen is bumped whenever desired changes, letting an
// in-flight probe discard its result if a newer load superseded it.
type attestState struct {
	mu      sync.Mutex
	desired *RoleTokens
	current *RoleTokens
	gen     uint64
}

func newAttestState() *attestState { return &attestState{} }

func (a *attestState) get() (*RoleTokens, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current, a.current != nil
}

// setDesired records a new target (clone) without changing the active snapshot;
// the next probe drives current toward it. Bumps gen.
func (a *attestState) setDesired(rt *RoleTokens) {
	c := rt.clone()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.desired = c
	a.gen++
}

// publish makes rt the desired AND active snapshot at once (success building
// block; also used by tests).
func (a *attestState) publish(rt *RoleTokens) {
	c := rt.clone()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.desired = c
	a.current = c
	a.gen++
}

// probeTarget returns the desired snapshot to probe and the gen it was read at.
func (a *attestState) probeTarget() (*RoleTokens, uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.desired, a.gen
}

// applyProbe applies a probe outcome iff desired has not changed since gen
// (otherwise the result is stale and dropped). Success activates d; failure
// fails closed.
func (a *attestState) applyProbe(gen uint64, d *RoleTokens, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gen != gen {
		return // a newer load superseded this probe
	}
	if ok {
		a.current = d
	} else {
		a.current = nil
	}
}

// currentRoleTokens returns the active attested snapshot, if any.
func (s *Server) currentRoleTokens() (*RoleTokens, bool) { return s.attest.get() }

// attestNow probes the current desired snapshot and applies the outcome
// (success → active, failure → fail closed). Stale results (a newer desired
// landed mid-probe) are dropped by applyProbe's gen check.
func (s *Server) attestNow(ctx context.Context) error {
	d, gen := s.attest.probeTarget()
	if d == nil {
		return fmt.Errorf("no role tokens configured")
	}
	err := attestProbe(ctx, s.cfg.AdminURL, d)
	s.attest.applyProbe(gen, d, err == nil)
	return err
}

// StartReprobe periodically re-attests the intended snapshot. On failure it
// clears the active snapshot (proxyAdmin/readyz fail closed); on success it
// republishes if cleared (recovery). It also recovers from a cold-start failure
// where nothing ever published by falling back to the configured startup tokens.
// A gen check ensures a probe result is dropped if a reload superseded it.
func (s *Server) StartReprobe(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		s.log.Error("attestation re-probe disabled: non-positive interval", "interval", interval)
		return
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.attestNow(ctx); err != nil {
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
