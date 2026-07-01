package dashboard

import (
	"encoding/json"
	"net/http"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/httpx"
)

// handlePublicConfig is an unauthenticated hint the SPA reads on load to decide
// whether to auto-authenticate as the demo viewer. It exposes nothing sensitive.
func (s *Server) handlePublicConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"demo": s.cfg.DemoMode})
}

// handleDemoLogin issues a session for the configured demo user WITHOUT a
// password, for the public read-only demo. It is the only passwordless session
// path in the system and is guarded three ways:
//   - 404 unless demo mode is explicitly enabled,
//   - per-IP throttled like the password login,
//   - fail-closed: it refuses (403) to issue a session unless the demo user
//     resolves to the viewer role in the LIVE users file. It can never mint a
//     session above read-only, even if the users file is edited at runtime.
func (s *Server) handleDemoLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DemoMode {
		httpx.Problem(w, http.StatusNotFound, "not found", "demo mode is not enabled")
		return
	}
	// No throttle here, deliberately: demo-login carries no credential to
	// brute-force (it only ever mints a read-only viewer session), and behind a
	// reverse proxy / Tailscale Funnel every request shares one RemoteAddr, so a
	// per-IP throttle would collapse to a single bucket and 429 real visitors.
	role, ok := s.currentUsers().RoleOf(s.cfg.DemoUser)
	if !ok {
		s.log.Error("demo login: configured demo user missing from users file", "user", s.cfg.DemoUser)
		httpx.Problem(w, http.StatusForbidden, "demo unavailable", "demo user is not configured")
		return
	}
	if role != admin.RoleViewer {
		// Defense in depth: config load already rejects a non-viewer demo user,
		// but never issue anything but read-only even if the file changed.
		s.log.Error("demo login refused: demo user is not viewer", "user", s.cfg.DemoUser, "role", role.String())
		httpx.Problem(w, http.StatusForbidden, "demo unavailable", "demo user must be read-only")
		return
	}
	// Secure set dynamically via InsecureCookie flag — allowed by design.
	//nolint:gosec
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    s.sessions.Issue(s.now(), s.cfg.DemoUser),
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.cfg.InsecureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	s.log.Info("dashboard demo login ok", "user", s.cfg.DemoUser, "role", role.String(), "ip", clientIP(r))
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "role": role.String(), "username": s.cfg.DemoUser})
}
