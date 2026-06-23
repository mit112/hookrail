package dashboard

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMem     = 65536 // KiB (64 MiB)
	argonTime    = 2
	argonPar     = 4
	argonSaltLen = 16
	argonKeyLen  = 32
)

func hashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMem, argonPar, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMem, argonTime, argonPar,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// HashPassword is the exported entry point for the hookrail-dash-hash CLI.
func HashPassword(pw string) (string, error) { return hashPassword(pw) }

// verifyPassword recomputes the argon2id key using the params embedded in phc
// and compares in constant time. Any parse failure returns false.
func verifyPassword(pw, phc string) bool {
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var mem, tcost uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &tcost, &par); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 64 {
		return false
	}
	// len(want) is bounded to [1,64] above, so the uint32 conversion cannot overflow.
	got := argon2.IDKey([]byte(pw), salt, tcost, mem, par, uint32(len(want))) //nolint:gosec
	return subtle.ConstantTimeCompare(got, want) == 1
}

// decoyPHC is a valid PHC for a random password, computed once, used to keep
// login timing uniform when a username is unknown (no early return).
var decoyPHC = func() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	h, _ := hashPassword(string(b))
	return h
}()

func decoyHash() string { return decoyPHC }
