package admin

import "testing"

func TestRoleRankAndParse(t *testing.T) {
	if !(RoleViewer < RoleOperator && RoleOperator < RoleAdmin) {
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
