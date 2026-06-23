package admin

import (
	"encoding/json"
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

// whoami returns the authenticated principal's role. The dashboard BFF uses it
// to attest that each configured role token is actually scoped to its declared
// role (RBAC R2, D11). Read-only and side-effect free.
func (s *Server) whoami(w http.ResponseWriter, r *http.Request) {
	// authz already authenticated the caller and stashed the principal; reuse it
	// (no second store lookup, no nondeterministic re-auth failure).
	p, ok := principalFrom(r.Context())
	if !ok { // unreachable behind authz, but fail closed
		httpx.Problem(w, http.StatusServiceUnavailable, "not ready", "could not verify credentials")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"role": p.role.String()})
}
