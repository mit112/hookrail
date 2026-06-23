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

// Reload re-reads both secret files. The user map swaps atomically on a clean
// parse; the role-token map is staged, attested, and published only on success.
// Old state is retained on any error (never crash, never publish unattested).
func (s *Server) Reload(ctx context.Context) error {
	var errs []error
	if u, err := LoadUsers(s.cfg.UsersFile); err != nil {
		errs = append(errs, err)
	} else {
		s.usersPtr.Store(u)
	}
	if rt, err := LoadRoleTokens(s.cfg.RoleTokensFile); err != nil {
		errs = append(errs, err)
	} else {
		// Always record the new tokens as the desired target so the re-probe
		// recovers to THEM (not stale ones) even if this attest fails transiently.
		s.attest.setDesired(rt)
		if err := s.attestNow(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
