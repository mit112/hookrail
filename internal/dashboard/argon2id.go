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

// PHC cost is pinned to the exact canonical cost emitted by hashPassword
// (m=argonMem,t=argonTime,p=argonPar). A users-file entry with any other cost is
// rejected at load: this prevents a "valid" but expensive entry from driving
// request-amplified argon2 work on every login, and keeps the uniform-failure
// decoy (which uses the canonical cost) timing-matched to real entries. Salt and
// key lengths are bounded (length variation is not a DoS vector).
const (
	phcMinSalt = 8
	phcMinKey  = 16
	phcMaxKey  = 64
)

type phcParts struct {
	mem  uint32
	time uint32
	par  uint8
	salt []byte
	key  []byte
}

// parsePHC parses and bounds-checks an argon2id PHC string. It rejects any
// out-of-policy value so callers never feed unsafe params to argon2.IDKey.
func parsePHC(phc string) (phcParts, error) {
	var p phcParts
	parts := strings.Split(phc, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, fmt.Errorf("not an argon2id PHC string")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return p, fmt.Errorf("bad argon2 version")
	}
	var trailing string
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d%s", &p.mem, &p.time, &p.par, &trailing); err != nil && n < 3 {
		return p, fmt.Errorf("bad argon2 params")
	}
	if trailing != "" {
		return p, fmt.Errorf("trailing data in argon2 params")
	}
	if p.mem != argonMem || p.time != argonTime || p.par != argonPar {
		return p, fmt.Errorf("argon2 cost must equal the canonical m=%d,t=%d,p=%d", argonMem, argonTime, argonPar)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < phcMinSalt {
		return p, fmt.Errorf("bad argon2 salt")
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < phcMinKey || len(key) > phcMaxKey {
		return p, fmt.Errorf("bad argon2 key")
	}
	p.salt, p.key = salt, key
	return p, nil
}

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

// validPHC reports whether phc is a well-formed, in-policy argon2id hash.
// Used at users-file load time so a bad hash fails closed at startup.
func validPHC(phc string) bool {
	_, err := parsePHC(phc)
	return err == nil
}

// verifyPassword recomputes the argon2id key using the bounds-checked params
// embedded in phc and compares in constant time. Any parse failure returns false.
func verifyPassword(pw, phc string) bool {
	p, err := parsePHC(phc)
	if err != nil {
		return false
	}
	// len(p.key) is bounded to [phcMinKey,phcMaxKey] by parsePHC.
	got := argon2.IDKey([]byte(pw), p.salt, p.time, p.mem, p.par, uint32(len(p.key))) //nolint:gosec
	return subtle.ConstantTimeCompare(got, p.key) == 1
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
