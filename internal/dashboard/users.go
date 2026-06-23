package dashboard

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mit112/hookrail/internal/admin"
)

type userRec struct {
	hash string
	role admin.Role
}

// Users is an immutable, parsed view of the dashboard users file.
type Users struct{ m map[string]userRec }

func LoadUsers(path string) (*Users, error) {
	f, err := os.Open(path) //nolint:gosec // path from env, by design
	if err != nil {
		return nil, fmt.Errorf("opening users file: %w", err)
	}
	defer f.Close() //nolint:errcheck
	return ParseUsers(f)
}

// ParseUsers parses `username:argon2id-phc:role` lines (comments with #, blanks
// skipped). It fails closed on any malformed line, duplicate user, or empty set.
func ParseUsers(r io.Reader) (*Users, error) {
	u := &Users{m: map[string]userRec{}}
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		parts := strings.Split(raw, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("users line %d: expected username:hash:role", line)
		}
		name, hash, roleStr := parts[0], parts[1], parts[2]
		if name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("users line %d: invalid username", line)
		}
		if !validPHC(hash) {
			return nil, fmt.Errorf("users line %d: hash is not a valid in-policy argon2id PHC", line)
		}
		role, ok := admin.ParseRole(roleStr)
		if !ok {
			return nil, fmt.Errorf("users line %d: invalid role %q", line, roleStr)
		}
		if _, dup := u.m[name]; dup {
			return nil, fmt.Errorf("users line %d: duplicate username %q", line, name)
		}
		u.m[name] = userRec{hash: hash, role: role}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(u.m) == 0 {
		return nil, fmt.Errorf("users file defines no users")
	}
	return u, nil
}

func (u *Users) Len() int { return len(u.m) }

// Verify always runs an argon2id comparison (decoy on miss) to keep timing
// uniform and avoid username enumeration.
func (u *Users) Verify(username, password string) (admin.Role, bool) {
	rec, ok := u.m[username]
	if !ok {
		_ = verifyPassword(password, decoyHash())
		return 0, false
	}
	if !verifyPassword(password, rec.hash) {
		return 0, false
	}
	return rec.role, true
}

// RoleOf returns the current role for username (used per-request; never the cookie).
func (u *Users) RoleOf(username string) (admin.Role, bool) {
	rec, ok := u.m[username]
	if !ok {
		return 0, false
	}
	return rec.role, true
}
