package dashboard

import (
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

func TestParseRoleTokensValid(t *testing.T) {
	rt, err := ParseRoleTokens(strings.NewReader(goodRoleTokensFile()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tok, ok := rt.For(admin.RoleOperator); !ok || tok != tokOperator {
		t.Fatalf("operator token: ok=%v tok=%q", ok, tok)
	}
	if len(rt.Roles()) != 3 {
		t.Fatalf("want 3 roles, got %d", len(rt.Roles()))
	}
}

func TestParseRoleTokensRejectsBad(t *testing.T) {
	cases := map[string]string{
		"missing role":   "viewer:" + tokViewer + "\noperator:" + tokOperator + "\n",
		"bad prefix":     "viewer:nope\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"bare prefix":    "viewer:hkadm_\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"short hex":      "viewer:hkadm_deadbeef\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"uppercase hex":  "viewer:hkadm_" + strings.Repeat("A", 48) + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"trailing colon": "viewer:" + tokViewer + ":x\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"empty token":    "viewer:\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"duplicate role": "viewer:" + tokViewer + "\nviewer:" + tokAdmin + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
		"unknown role":   "root:" + tokViewer + "\noperator:" + tokOperator + "\nadmin:" + tokAdmin + "\n",
	}
	for name, src := range cases {
		if _, err := ParseRoleTokens(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
