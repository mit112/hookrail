package dashboard

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

// InitialAttest probes + publishes the startup role tokens. Callers may start
// serving even if this errors: proxyAdmin/readyz fail closed until an attested
// snapshot exists, and the periodic re-probe will recover when the admin API is
// reachable.
func (s *Server) InitialAttest(ctx context.Context) error {
	s.attest.setDesired(s.cfg.RoleTokens)
	return s.attestNow(ctx)
}

// Reload re-reads both secret files atomically: BOTH must parse before anything
// is applied, so a read/parse error in either file changes nothing (no partial
// apply of users against stale tokens). Once both parse, the user map swaps and
// the new tokens become the desired target; attestation then activates them or
// fails closed (desired still tracks the new tokens for re-probe recovery).
func (s *Server) Reload(ctx context.Context) error {
	u, uerr := LoadUsers(s.cfg.UsersFile)
	rt, rerr := LoadRoleTokens(s.cfg.RoleTokensFile)
	if uerr != nil || rerr != nil {
		return errors.Join(uerr, rerr) // keep both old; apply nothing
	}
	// Users are swapped BEFORE attestation completes, by design. This prioritizes
	// immediate revocation (a deleted/downgraded user loses access on the next
	// request even if the new token set fails to attest — the core D3 goal).
	// Deferring the user swap until after attest would re-open that revocation gap
	// on any attest failure. The interim "new user + previous token" window is
	// safe: a user's role comes from the users map, role tokens are role-keyed,
	// and the still-active snapshot holds the PREVIOUS *attested* tokens for that
	// role — so the user gets exactly their granted role via an already-verified,
	// correctly-role-scoped credential (no escalation, no D11 violation). A
	// mislabeled new token set fails closed (503) once attestNow runs.
	s.usersPtr.Store(u)
	s.attest.setDesired(rt)
	return s.attestNow(ctx)
}

// InstallSIGHUP reloads both secret files on SIGHUP, keeping previous state on
// any failure. Bounds revocation without a full process restart (RBAC R2, D3).
func (s *Server) InstallSIGHUP(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if err := s.Reload(ctx); err != nil {
					s.log.Error("dashboard reload failed (keeping previous state)", "err", err)
				} else {
					s.log.Info("dashboard reload ok")
				}
			}
		}
	}()
}
