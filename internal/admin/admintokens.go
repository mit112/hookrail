package admin

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// Stub handlers — registered (admin-only) in M2 so the route policy is complete;
// real implementations land in M3 (Task 7).

func (s *Server) createAdminToken(w http.ResponseWriter, _ *http.Request) {
	httpx.Problem(w, http.StatusNotImplemented, "not implemented", "admin-token create lands in M3")
}

func (s *Server) listAdminTokens(w http.ResponseWriter, _ *http.Request) {
	httpx.Problem(w, http.StatusNotImplemented, "not implemented", "admin-token list lands in M3")
}

func (s *Server) revokeAdminToken(w http.ResponseWriter, _ *http.Request) {
	httpx.Problem(w, http.StatusNotImplemented, "not implemented", "admin-token revoke lands in M3")
}
