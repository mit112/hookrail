package dashboard

import (
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

// TestDashboardPolicyMirrorsAdminRegistry is the bidirectional drift guard:
// the proxied dashboard allowlist must equal the upstream admin registry MINUS
// the intentional exclusions (token management + whoami), with identical roles.
func TestDashboardPolicyMirrorsAdminRegistry(t *testing.T) {
	upstream := map[string]admin.Role{}
	for _, ri := range (&admin.Server{}).RouteTable() {
		key := ri.Method + " " + ri.Pattern
		if notProxied[key] {
			continue
		}
		upstream[key] = ri.MinRole
	}
	dash := map[string]admin.Role{}
	s := &Server{}
	for _, rt := range s.adminRoutes() {
		key := rt.method + " " + rt.path
		if notProxied[key] {
			t.Errorf("route %q must NOT be on the dashboard allowlist", key)
		}
		dash[key] = rt.minRole
	}
	// Every proxied route exists upstream with the same role.
	for key, role := range dash {
		want, ok := upstream[key]
		if !ok {
			t.Errorf("proxied route %q not present upstream (or is excluded)", key)
			continue
		}
		if role != want {
			t.Errorf("route %q minRole=%v upstream=%v (drift)", key, role, want)
		}
	}
	// Every non-excluded upstream route is proxied (no silent omissions).
	for key, want := range upstream {
		if _, ok := dash[key]; !ok {
			t.Errorf("upstream route %q (minRole %v) is missing from the dashboard allowlist", key, want)
		}
	}
}
