package dashboard

import (
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/admin"
)

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestParseUsersValid(t *testing.T) {
	src := "# comment\n\nalice:" + mustHash(t, "pw-alice") + ":admin\nbob:" + mustHash(t, "pw-bob") + ":viewer\n"
	u, err := ParseUsers(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Len() != 2 {
		t.Fatalf("want 2 users, got %d", u.Len())
	}
	if r, ok := u.Verify("alice", "pw-alice"); !ok || r != admin.RoleAdmin {
		t.Fatalf("alice verify: ok=%v role=%v", ok, r)
	}
	if _, ok := u.Verify("alice", "wrong"); ok {
		t.Fatal("wrong password must fail")
	}
	if _, ok := u.Verify("nobody", "x"); ok {
		t.Fatal("unknown user must fail")
	}
	if r, ok := u.RoleOf("bob"); !ok || r != admin.RoleViewer {
		t.Fatalf("RoleOf(bob): ok=%v role=%v", ok, r)
	}
	if _, ok := u.RoleOf("nobody"); ok {
		t.Fatal("RoleOf unknown must be false")
	}
}

func TestParseUsersRejectsBad(t *testing.T) {
	cases := map[string]string{
		"empty file":     "# only a comment\n",
		"bad role":       "x:" + mustHash(t, "p") + ":superuser\n",
		"too few fields": "x:" + mustHash(t, "p") + "\n",
		"malformed hash": "x:not-a-phc:viewer\n",
		"blank username": ":" + mustHash(t, "p") + ":viewer\n",
		"ws in username": "a b:" + mustHash(t, "p") + ":viewer\n",
	}
	for name, src := range cases {
		if _, err := ParseUsers(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected parse error", name)
		}
	}
}

func TestParseUsersRejectsDuplicate(t *testing.T) {
	src := "a:" + mustHash(t, "p1") + ":admin\na:" + mustHash(t, "p2") + ":viewer\n"
	if _, err := ParseUsers(strings.NewReader(src)); err == nil {
		t.Fatal("duplicate username must error")
	}
}
