// Package config is 12-factor env config (§4: env + flags, no config service).
package config

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL string   // DATABASE_URL
	RedisAddr   string   // REDIS_ADDR (host:port or redis:// URL)
	Stream      string   // HOOKRAIL_STREAM, default "hookrail:deliveries"
	Group       string   // HOOKRAIL_GROUP, default "deliverers"
	ListenAddr  string   // HOOKRAIL_LISTEN, default ":8080"
	MasterKey   [32]byte // HOOKRAIL_MASTER_KEY: 64 hex chars (§4 secrets)
	AllowHTTP   bool     // HOOKRAIL_ALLOW_HTTP=true (dev only, §8)
	AllowCIDRs  []string // HOOKRAIL_ALLOW_CIDRS: comma-separated allowlist (§8)
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   os.Getenv("REDIS_ADDR"),
		Stream:      envOr("HOOKRAIL_STREAM", "hookrail:deliveries"),
		Group:       envOr("HOOKRAIL_GROUP", "deliverers"),
		ListenAddr:  envOr("HOOKRAIL_LISTEN", ":8080"),
		AllowHTTP:   os.Getenv("HOOKRAIL_ALLOW_HTTP") == "true",
	}
	if c.DatabaseURL == "" || c.RedisAddr == "" {
		return c, fmt.Errorf("DATABASE_URL and REDIS_ADDR are required")
	}
	mk := os.Getenv("HOOKRAIL_MASTER_KEY")
	raw, err := hex.DecodeString(mk)
	if err != nil || len(raw) != 32 {
		return c, fmt.Errorf("HOOKRAIL_MASTER_KEY must be 64 hex chars (32 bytes)")
	}
	copy(c.MasterKey[:], raw)
	if v := os.Getenv("HOOKRAIL_ALLOW_CIDRS"); v != "" {
		c.AllowCIDRs = strings.Split(v, ",")
	}
	return c, nil
}

// AllowPrefixes parses HOOKRAIL_ALLOW_CIDRS into the SSRF allowlist form.
// Every component that builds an ssrf.Policy (worker, ctl) MUST use this so
// registration-time and dial-time checks agree (§8).
func (c Config) AllowPrefixes() ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(c.AllowCIDRs))
	for _, s := range c.AllowCIDRs {
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("HOOKRAIL_ALLOW_CIDRS entry %q: %w", s, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
