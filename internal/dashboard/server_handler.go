// internal/dashboard/server_handler.go
package dashboard

import "net/http"

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// auth (login public; logout/session guarded)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.requireSession(s.handleLogout))
	mux.HandleFunc("GET /api/session", s.requireSession(s.handleSession))
	mux.HandleFunc("POST /api/test-event", s.requireSession(s.handleTestEvent))
	// allowlist admin proxy
	for _, rt := range s.adminRoutes() {
		mux.HandleFunc(rt.method+" "+rt.path, s.requireSession(s.proxyAdmin))
	}
	// ops (open)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", s.readyz)
	// static SPA + fallback (public shell)
	mux.HandleFunc("GET /", s.serveStatic)
	return mux
}
