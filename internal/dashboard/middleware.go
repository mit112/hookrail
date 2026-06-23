package dashboard

import (
	"net/http"

	"github.com/mit112/hookrail/internal/httpx"
)

func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.cookieName())
		if err != nil {
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "log in first")
			return
		}
		sub, ok := s.sessions.Valid(c.Value, s.now())
		if !ok {
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "log in first")
			return
		}
		// Re-resolve the subject against the live user set so a deleted user's
		// outstanding cookie is rejected immediately (RBAC R2 revocation, D3).
		if _, ok := s.currentUsers().RoleOf(sub); !ok {
			httpx.Problem(w, http.StatusUnauthorized, "not authenticated", "user no longer exists")
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
		next(w, r)
	}
}

func originOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
