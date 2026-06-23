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

// attestState holds the active, attested role-token snapshot. It is nil until
// the first successful attestation; proxyAdmin fails closed (503) while nil.
type attestState struct {
	current atomic.Pointer[RoleTokens]
}

func newAttestState() *attestState { return &attestState{} }

func (a *attestState) get() (*RoleTokens, bool) {
	rt := a.current.Load()
	return rt, rt != nil
}

// publish stores an independent clone so the active snapshot is immutable and
// decoupled from any caller-held RoleTokens reference.
func (a *attestState) publish(rt *RoleTokens) { a.current.Store(rt.clone()) }

func (a *attestState) clear() { a.current.Store(nil) }

// currentRoleTokens returns the active attested snapshot, if any.
func (s *Server) currentRoleTokens() (*RoleTokens, bool) { return s.attest.get() }

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
