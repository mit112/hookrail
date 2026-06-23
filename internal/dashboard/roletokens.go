package dashboard

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/mit112/hookrail/internal/admin"
)

// adminTokenRe matches the exact minted admin-token syntax: "hkadm_" + 48 hex
// chars (24 random bytes hex-encoded). Validated at parse time so a fat-fingered
// token fails closed at startup instead of becoming a delayed per-role outage.
var adminTokenRe = regexp.MustCompile(`^hkadm_[0-9a-f]{48}$`)

// RoleTokens maps each role to its upstream admin token (hkadm_…). All three
// roles are required; the token's actual role is later attested via /v1/whoami.
type RoleTokens struct{ m map[admin.Role]string }

func LoadRoleTokens(path string) (*RoleTokens, error) {
	f, err := os.Open(path) //nolint:gosec // path from env, by design
	if err != nil {
		return nil, fmt.Errorf("opening role-tokens file: %w", err)
	}
	defer f.Close() //nolint:errcheck
	return ParseRoleTokens(f)
}

func ParseRoleTokens(r io.Reader) (*RoleTokens, error) {
	rt := &RoleTokens{m: map[admin.Role]string{}}
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		roleStr, tok, ok := strings.Cut(raw, ":")
		if !ok {
			return nil, fmt.Errorf("role-tokens line %d: expected role:token", line)
		}
		role, ok := admin.ParseRole(strings.TrimSpace(roleStr))
		if !ok {
			return nil, fmt.Errorf("role-tokens line %d: invalid role %q", line, roleStr)
		}
		tok = strings.TrimSpace(tok)
		if !adminTokenRe.MatchString(tok) {
			return nil, fmt.Errorf("role-tokens line %d: token must be a minted hkadm_ token (hkadm_ + 48 hex)", line)
		}
		if _, dup := rt.m[role]; dup {
			return nil, fmt.Errorf("role-tokens line %d: duplicate role", line)
		}
		rt.m[role] = tok
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, want := range []admin.Role{admin.RoleViewer, admin.RoleOperator, admin.RoleAdmin} {
		if _, ok := rt.m[want]; !ok {
			return nil, fmt.Errorf("role-tokens file missing role %q", want)
		}
	}
	return rt, nil
}

func (rt *RoleTokens) For(role admin.Role) (string, bool) {
	tok, ok := rt.m[role]
	return tok, ok
}

// clone returns a deep copy so a published (attested) snapshot is independent of
// any caller-held reference and can never be mutated in place under readers.
func (rt *RoleTokens) clone() *RoleTokens {
	m := make(map[admin.Role]string, len(rt.m))
	for k, v := range rt.m {
		m[k] = v
	}
	return &RoleTokens{m: m}
}

// Roles returns the configured roles in ascending privilege order.
func (rt *RoleTokens) Roles() []admin.Role {
	out := make([]admin.Role, 0, len(rt.m))
	for r := range rt.m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
