package dashboard

import (
	"context"
	"net/http"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/httpx"
)

type ctxKey int

const roleCtxKey ctxKey = 0

type principalCtx struct {
	sub  string
	role admin.Role
}

func withRole(ctx context.Context, sub string, role admin.Role) context.Context {
	return context.WithValue(ctx, roleCtxKey, principalCtx{sub: sub, role: role})
}

// requireRole validates the session, resolves the caller's LIVE role from the
// user file (never the cookie), enforces a per-route minimum role, applies the
// CSRF/content-type guards for mutating methods, and stashes (sub, role) in the
// request context for downstream handlers (proxy token selection, audit).
func (s *Server) requireRole(min admin.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub, ok := s.subject(r)
		if !ok {
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "log in first")
			return
		}
		role, ok := s.currentUsers().RoleOf(sub)
		if !ok { // user removed since the cookie was issued (revocation, D3)
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "user no longer exists")
			return
		}
		if role < min {
			httpx.Problem(w, http.StatusForbidden, "insufficient role", "requires role "+min.String())
			return
		}
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodDelete:
			if r.Header.Get("Content-Type") != "application/json" {
				httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != originOf(r) {
				httpx.Problem(w, http.StatusForbidden, "cross-origin", "origin not allowed")
				return
			}
		}
		next(w, r.WithContext(withRole(r.Context(), sub, role)))
	}
}
