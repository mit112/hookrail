package dashboard

import "testing"

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
