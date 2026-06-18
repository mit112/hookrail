// internal/admin/auth.go

// Package admin is the internal Admin/Query API (design §2): bearer-authed
// CRUD + query over the same Postgres truth the public ingress writes.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/mit112/hookrail/internal/httpx"
)

// digest reduces a token to a fixed 32-byte SHA-256 so the constant-time
// compare leaks neither value nor length (design §1.1).
func digest(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// authAdmin requires "Authorization: Bearer <HOOKRAIL_ADMIN_TOKEN>" on every
// /v1/* route. Ops routes (/healthz,/readyz,/metrics) are registered without
// this wrapper (design §1.1). Compares fixed digests in constant time.
func (s *Server) authAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			httpx.Problem(w, http.StatusUnauthorized, "missing credentials", "Authorization: Bearer <admin token> required")
			return
		}
		got := digest(raw)
		if subtle.ConstantTimeCompare(got[:], s.tokenDigest[:]) != 1 {
			httpx.Problem(w, http.StatusUnauthorized, "invalid credentials", "admin token rejected")
			return
		}
		next(w, r)
	}
}
