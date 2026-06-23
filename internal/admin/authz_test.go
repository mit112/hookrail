package admin

import "testing"

func TestRoleRankAndParse(t *testing.T) {
	if RoleViewer >= RoleOperator || RoleOperator >= RoleAdmin {
		t.Fatal("role ranks must be viewer < operator < admin")
	}
	cases := map[string]Role{"viewer": RoleViewer, "operator": RoleOperator, "admin": RoleAdmin}
	for s, want := range cases {
		got, ok := ParseRole(s)
		if !ok || got != want {
			t.Errorf("ParseRole(%q) = (%v,%v), want (%v,true)", s, got, ok, want)
		}
		if got.String() != s {
			t.Errorf("Role(%v).String() = %q, want %q", got, got.String(), s)
		}
	}
	if _, ok := ParseRole("superuser"); ok {
		t.Error("ParseRole(superuser) should fail")
	}
}

func TestRouteTablePolicy(t *testing.T) {
	// RouteTable() must not touch the store (zero-value Server is fine here).
	s := &Server{}
	want := map[string]Role{
		"GET /v1/endpoints":                     RoleViewer,
		"GET /v1/endpoints/{id}":                RoleViewer,
		"GET /v1/subscriptions":                 RoleViewer,
		"GET /v1/subscriptions/{id}":            RoleViewer,
		"GET /v1/dlq":                           RoleViewer,
		"GET /v1/deliveries":                    RoleViewer,
		"GET /v1/deliveries/{id}":               RoleViewer,
		"GET /v1/ordered-keys":                  RoleViewer,
		"POST /v1/dlq/{delivery_id}/replay":     RoleOperator,
		"POST /v1/deliveries/{id}/skip":         RoleOperator,
		"POST /v1/endpoints":                    RoleAdmin,
		"PATCH /v1/endpoints/{id}":              RoleAdmin,
		"DELETE /v1/endpoints/{id}":             RoleAdmin,
		"POST /v1/endpoints/{id}/rotate-secret": RoleAdmin,
		"POST /v1/subscriptions":                RoleAdmin,
		"PATCH /v1/subscriptions/{id}":          RoleAdmin,
		"DELETE /v1/subscriptions/{id}":         RoleAdmin,
		"POST /v1/admin-tokens":                 RoleAdmin,
		"GET /v1/admin-tokens":                  RoleAdmin,
		"DELETE /v1/admin-tokens/{id}":          RoleAdmin,
	}
	got := map[string]Role{}
	for _, rt := range s.RouteTable() {
		got[rt.Method+" "+rt.Pattern] = rt.MinRole
	}
	if len(got) != len(want) {
		t.Fatalf("RouteTable has %d routes, want %d", len(got), len(want))
	}
	for k, w := range want {
		if g, ok := got[k]; !ok || g != w {
			t.Errorf("route %q minRole = %v (present=%v), want %v", k, g, ok, w)
		}
	}
}
