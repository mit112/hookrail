// internal/dashboard/server_handler.go
package dashboard

import (
	"net/http"

	"github.com/mit112/hookrail/internal/admin"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// auth (login public; session self-checks; logout/test-event role-gated)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.requireRole(admin.RoleViewer, s.handleLogout))
	mux.HandleFunc("GET /api/session", s.handleSession)
	// public read-only demo: config hint (always) + passwordless viewer session
	// (404 unless demo mode is enabled; see demo.go for the fail-closed guards)
	mux.HandleFunc("GET /api/public-config", s.handlePublicConfig)
	mux.HandleFunc("POST /api/demo-login", s.handleDemoLogin)
	mux.HandleFunc("POST /api/test-event", s.requireRole(admin.RoleOperator, s.handleTestEvent))
	// allowlist admin proxy, each gated at its mirrored minimum role
	for _, rt := range s.adminRoutes() {
		mux.HandleFunc(rt.method+" "+rt.path, s.requireRole(rt.minRole, s.proxyAdmin))
	}
	// ops (open)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", s.readyz)
	// static SPA + fallback (public shell)
	mux.HandleFunc("GET /", s.serveStatic)
	return mux
}
