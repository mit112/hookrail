package dashboard

import (
	"strings"
	"testing"
)

// /v1/admin-tokens must NOT be on the proxy allowlist in R1 (RBAC design §5):
// the single-password dashboard must not be able to mint portable RBAC tokens
// before R2 introduces per-user dashboard roles. The BFF only proxies routes on
// adminRoutes(); anything else is never forwarded to the admin API.
func TestAdminTokensNotOnProxyAllowlist(t *testing.T) {
	s := &Server{}
	for _, r := range s.adminRoutes() {
		if strings.HasPrefix(r.path, "/v1/admin-tokens") {
			t.Fatalf("admin-tokens route %s %s is on the BFF allowlist; it must be direct-admin-only in R1", r.method, r.path)
		}
	}
}
