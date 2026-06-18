// Package config is 12-factor env config (§4: env + flags, no config service).
package config

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
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

	AdminToken            string        // HOOKRAIL_ADMIN_TOKEN (admin binary refuses boot if empty)
	AdminListen           string        // HOOKRAIL_ADMIN_LISTEN, default ":8082"
	IdemTTL               time.Duration // RETENTION_IDEM_HOURS, default 24h
	StreamMaxLen          int64         // RETENTION_STREAM_MAXLEN, default 100000
	EventPayloadRetention time.Duration // RETENTION_EVENT_PAYLOAD_DAYS, default 30d (job1 + replay-expiry)
	AttemptRetention      time.Duration // RETENTION_ATTEMPT_DAYS, default 30d (job2 + marker)
	RetentionInterval     time.Duration // RETENTION_INTERVAL, default 1h
	RetentionBatch        int           // RETENTION_BATCH, default 5000
	RetentionTickBudget   time.Duration // RETENTION_TICK_BUDGET, default 60s
	RetentionEnabled      bool          // RETENTION_ENABLED, default true
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

	c.AdminToken = os.Getenv("HOOKRAIL_ADMIN_TOKEN")
	c.AdminListen = envOr("HOOKRAIL_ADMIN_LISTEN", ":8082")

	var perr error
	posDur := func(env string, days bool, def time.Duration) time.Duration {
		raw := os.Getenv(env)
		if raw == "" {
			return def
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("%s must be a positive integer", env)
			return def
		}
		if days {
			return time.Duration(n) * 24 * time.Hour
		}
		return time.Duration(n) * time.Hour
	}
	c.EventPayloadRetention = posDur("RETENTION_EVENT_PAYLOAD_DAYS", true, 30*24*time.Hour)
	c.AttemptRetention = posDur("RETENTION_ATTEMPT_DAYS", true, 30*24*time.Hour)
	c.IdemTTL = posDur("RETENTION_IDEM_HOURS", false, 24*time.Hour)

	c.StreamMaxLen = 100_000
	if v := os.Getenv("RETENTION_STREAM_MAXLEN"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("RETENTION_STREAM_MAXLEN must be a positive integer")
		} else {
			c.StreamMaxLen = n
		}
	}
	c.RetentionInterval = time.Hour
	if v := os.Getenv("RETENTION_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			perr = fmt.Errorf("RETENTION_INTERVAL must be a positive Go duration")
		} else {
			c.RetentionInterval = d
		}
	}
	c.RetentionTickBudget = 60 * time.Second
	if v := os.Getenv("RETENTION_TICK_BUDGET"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			perr = fmt.Errorf("RETENTION_TICK_BUDGET must be a positive Go duration")
		} else {
			c.RetentionTickBudget = d
		}
	}
	c.RetentionBatch = 5000
	if v := os.Getenv("RETENTION_BATCH"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			perr = fmt.Errorf("RETENTION_BATCH must be a positive integer")
		} else {
			c.RetentionBatch = n
		}
	}
	c.RetentionEnabled = envOr("RETENTION_ENABLED", "true") == "true"
	if perr != nil {
		return c, perr
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
