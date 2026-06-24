package config

import (
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.IdemTTL != 24*time.Hour {
		t.Fatalf("IdemTTL default = %v", c.IdemTTL)
	}
	if c.StreamMaxLen != 100_000 {
		t.Fatalf("StreamMaxLen default = %d", c.StreamMaxLen)
	}
	if !c.RetentionEnabled || c.RetentionInterval != time.Hour {
		t.Fatalf("retention defaults wrong: enabled=%v interval=%v", c.RetentionEnabled, c.RetentionInterval)
	}
}

func TestLoadRejectsNonPositiveRetention(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("RETENTION_EVENT_PAYLOAD_DAYS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("zero RETENTION_EVENT_PAYLOAD_DAYS must fail startup (design §3)")
	}
}

func TestGlobalRateLimitDefaults(t *testing.T) {
	// With RedisAddr and no explicit flag: defaults to true.
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.GlobalRateLimit {
		t.Fatal("GlobalRateLimit should default to true when RedisAddr is set")
	}
	if c.RLTimeout != 50*time.Millisecond {
		t.Fatalf("RLTimeout default = %v", c.RLTimeout)
	}
	if c.RLTTLFloor != 60*time.Second {
		t.Fatalf("RLTTLFloor default = %v", c.RLTTLFloor)
	}
	if c.LimitsRefreshInterval != 15*time.Second {
		t.Fatalf("LimitsRefreshInterval default = %v", c.LimitsRefreshInterval)
	}
}

func TestLimitsRefreshIntervalOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("HOOKRAIL_LIMITS_REFRESH_INTERVAL", "1s")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LimitsRefreshInterval != time.Second {
		t.Fatalf("LimitsRefreshInterval = %v, want 1s", c.LimitsRefreshInterval)
	}
}

func TestDBConnectTimeout_DefaultAndOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DBConnectTimeout != 30*time.Second {
		t.Fatalf("default DBConnectTimeout = %v, want 30s", c.DBConnectTimeout)
	}
	t.Setenv("HOOKRAIL_DB_CONNECT_TIMEOUT", "5s")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DBConnectTimeout != 5*time.Second {
		t.Fatalf("override DBConnectTimeout = %v, want 5s", c.DBConnectTimeout)
	}
	for _, bad := range []string{"nonsense", "0", "-1s"} {
		t.Setenv("HOOKRAIL_DB_CONNECT_TIMEOUT", bad)
		if _, err := Load(); err == nil {
			t.Fatalf("expected error on HOOKRAIL_DB_CONNECT_TIMEOUT=%q", bad)
		}
	}
}

func TestLimitsRefreshIntervalRejectsBad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("HOOKRAIL_LIMITS_REFRESH_INTERVAL", "nope")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid HOOKRAIL_LIMITS_REFRESH_INTERVAL")
	}
}

func TestGlobalRateLimitDisabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("HOOKRAIL_GLOBAL_RATELIMIT", "0")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.GlobalRateLimit {
		t.Fatal("GlobalRateLimit should be false when HOOKRAIL_GLOBAL_RATELIMIT=0")
	}
}

func TestGlobalRateLimitOverrides(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("HOOKRAIL_RL_TIMEOUT_MS", "100")
	t.Setenv("HOOKRAIL_RL_TTL_FLOOR_S", "120")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.RLTimeout != 100*time.Millisecond {
		t.Fatalf("RLTimeout = %v", c.RLTimeout)
	}
	if c.RLTTLFloor != 120*time.Second {
		t.Fatalf("RLTTLFloor = %v", c.RLTTLFloor)
	}
}

func TestRLRedisAddrDefaultsToRedisAddr(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REDIS_ADDR", "redis.example.com:6379")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RLRedisAddr != "redis.example.com:6379" {
		t.Fatalf("default RLRedisAddr = %q, want redis.example.com:6379", cfg.RLRedisAddr)
	}
}

func TestRLRedisAddrOverride(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REDIS_ADDR", "redis.example.com:6379")
	t.Setenv("HOOKRAIL_RL_REDIS_ADDR", "toxiproxy:8479")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RLRedisAddr != "toxiproxy:8479" {
		t.Fatalf("RLRedisAddr = %q, want toxiproxy:8479", cfg.RLRedisAddr)
	}
}

func TestLoad_SentinelMode_NoRedisAddr(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_SENTINEL_ADDRS", "s1:26379,s2:26379,s3:26379")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.RedisConfigured {
		t.Fatal("RedisConfigured should be true in sentinel mode")
	}
	if len(c.RedisSentinelAddrs) != 3 || c.RedisMasterName != "hookrail" {
		t.Fatalf("sentinel addrs=%v master=%q", c.RedisSentinelAddrs, c.RedisMasterName)
	}
	if !c.GlobalRateLimit {
		t.Fatal("GlobalRateLimit should default on when RedisConfigured")
	}
}

func TestLoad_NoRedisAtAll_Errors(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_SENTINEL_ADDRS", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when neither REDIS_ADDR nor REDIS_SENTINEL_ADDRS set")
	}
}

func TestLoad_RLRedisAddr_IgnoredInSentinelMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("REDIS_SENTINEL_ADDRS", "s1:26379")
	t.Setenv("HOOKRAIL_RL_REDIS_ADDR", "toxi:6379")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.RLRedisAddr != "" {
		t.Fatalf("RLRedisAddr should be empty/ignored in sentinel mode, got %q", c.RLRedisAddr)
	}
}

func TestLoad_BothSet_SentinelWins(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("HOOKRAIL_MASTER_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	t.Setenv("REDIS_ADDR", "plain:6379")
	t.Setenv("REDIS_SENTINEL_ADDRS", "s1:26379,s2:26379")
	t.Setenv("HOOKRAIL_RL_REDIS_ADDR", "toxi:6379")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.RedisConfigured || len(c.RedisSentinelAddrs) != 2 {
		t.Fatalf("sentinel should be active: configured=%v addrs=%v", c.RedisConfigured, c.RedisSentinelAddrs)
	}
	// Sentinel mode wins: the plain-mode RL override must be ignored even though
	// REDIS_ADDR is also set.
	if c.RLRedisAddr != "" {
		t.Fatalf("RLRedisAddr=%q want empty (sentinel wins, plain RL override ignored)", c.RLRedisAddr)
	}
}
