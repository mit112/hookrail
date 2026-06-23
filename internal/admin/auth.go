// internal/admin/auth.go

// Package admin is the internal Admin/Query API (design §2): role-scoped
// bearer-authed CRUD + query over the same Postgres truth the public ingress
// writes. Authorization is enforced per-route by authz() in authz.go.
package admin

import (
	"crypto/sha256"
	"crypto/subtle"
)

// digest reduces a token to a fixed 32-byte SHA-256 so the constant-time
// compare leaks neither value nor length (design §1.1).
func digest(token string) [32]byte { return sha256.Sum256([]byte(token)) }

// subtleConstantTimeEq compares two fixed digests in constant time.
func subtleConstantTimeEq(a, b [32]byte) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
