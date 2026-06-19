// internal/dashboard/static.go
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	// The `GET /` pattern is a catch-all; an unknown API path must 404, NOT fall back to the SPA shell
	// (otherwise unknown /v1/* and /api/* would 200 with index.html and the open-proxy 404 test fails).
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	sub, _ := fs.Sub(distFS, "dist")
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" { p = "index.html" }
	if _, err := fs.Stat(sub, p); err != nil {
		p = "index.html" // SPA client-side route fallback
	}
	// Path is constrained to the embedded dist filesystem — traversal-safe.
	//nolint:gosec
	http.ServeFileFS(w, r, sub, p)
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	// best-effort: report ready (admin reachability proven by the proxy on demand)
	w.WriteHeader(http.StatusOK)
}
