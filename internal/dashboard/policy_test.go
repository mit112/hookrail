package dashboard

import (
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

// TestDashboardPolicyMatchesAdminRegistry is the drift guard: every proxied
// dashboard route must exist upstream with the SAME minimum role, and the
// dashboard must never proxy admin-tokens or whoami.
func TestDashboardPolicyMatchesAdminRegistry(t *testing.T) {
	upstream := map[string]admin.Role{}
	for _, ri := range (&admin.Server{}).RouteTable() {
		upstream[ri.Method+" "+ri.Pattern] = ri.MinRole
	}
	s := &Server{}
	for _, rt := range s.adminRoutes() {
		key := rt.method + " " + rt.path
		want, ok := upstream[key]
		if !ok {
			t.Errorf("proxied route %q not present upstream", key)
			continue
		}
		if rt.minRole != want {
			t.Errorf("route %q minRole=%v upstream=%v (drift)", key, rt.minRole, want)
		}
		switch rt.path {
		case "/v1/admin-tokens", "/v1/admin-tokens/{id}", "/v1/whoami":
			t.Errorf("route %q must NOT be on the dashboard allowlist", rt.path)
		}
	}
}
