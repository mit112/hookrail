package dashboard

import (
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

func TestParseRoleTokensValid(t *testing.T) {
	rt, err := ParseRoleTokens(strings.NewReader("viewer:hkadm_v\noperator:hkadm_o\nadmin:hkadm_a\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tok, ok := rt.For(admin.RoleOperator); !ok || tok != "hkadm_o" {
		t.Fatalf("operator token: ok=%v tok=%q", ok, tok)
	}
	if len(rt.Roles()) != 3 {
		t.Fatalf("want 3 roles, got %d", len(rt.Roles()))
	}
}

func TestParseRoleTokensRejectsBad(t *testing.T) {
	cases := map[string]string{
		"missing role":   "viewer:hkadm_v\noperator:hkadm_o\n",
		"bad prefix":     "viewer:nope\noperator:hkadm_o\nadmin:hkadm_a\n",
		"empty token":    "viewer:\noperator:hkadm_o\nadmin:hkadm_a\n",
		"duplicate role": "viewer:hkadm_v\nviewer:hkadm_x\noperator:hkadm_o\nadmin:hkadm_a\n",
		"unknown role":   "root:hkadm_r\noperator:hkadm_o\nadmin:hkadm_a\n",
	}
	for name, src := range cases {
		if _, err := ParseRoleTokens(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
