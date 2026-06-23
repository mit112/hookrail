package dashboard

import (
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !verifyPassword("correct horse battery staple", h) {
		t.Fatal("correct password should verify")
	}
	if verifyPassword("wrong", h) {
		t.Fatal("wrong password must not verify")
	}
}

func TestVerifyRejectsMalformedPHC(t *testing.T) {
	for _, bad := range []string{"", "notphc", "$argon2id$v=19$bad$x$y", "$2a$bcrypt"} {
		if verifyPassword("x", bad) {
			t.Fatalf("malformed PHC %q must not verify", bad)
		}
	}
}

func TestDecoyNeverMatchesEmpty(t *testing.T) {
	if verifyPassword("", decoyHash()) {
		t.Fatal("decoy must not verify the empty password")
	}
}

// A real hash provides a valid salt+key segment we can splice bad params onto.
func paramsSplice(t *testing.T, params string) string {
	t.Helper()
	h := mustHash(t, "x")
	parts := strings.Split(h, "$") // ["","argon2id","v=19","m=..,t=..,p=..",salt,key]
	parts[3] = params
	return strings.Join(parts, "$")
}

func TestValidPHCRejectsOutOfPolicy(t *testing.T) {
	bad := map[string]string{
		"t=0":           paramsSplice(t, "m=65536,t=0,p=4"),
		"p=0":           paramsSplice(t, "m=65536,t=2,p=0"),
		"huge m":        paramsSplice(t, "m=4294967295,t=2,p=4"),
		"tiny m":        paramsSplice(t, "m=1,t=2,p=4"),
		"m<8p":          paramsSplice(t, "m=16,t=2,p=4"),
		"trailing":      paramsSplice(t, "m=65536,t=2,p=4,x=1"),
		"bad base64":    "$argon2id$v=19$m=65536,t=2,p=4$!!!$@@@",
		"wrong version": strings.Replace(mustHash(t, "x"), "v=19", "v=18", 1),
	}
	for name, phc := range bad {
		if validPHC(phc) {
			t.Errorf("%s: validPHC should be false", name)
		}
		if verifyPassword("x", phc) {
			t.Errorf("%s: verifyPassword should be false", name)
		}
	}
	// Sanity: a real hash is in policy.
	if !validPHC(mustHash(t, "x")) {
		t.Fatal("a freshly minted hash must be valid")
	}
}
