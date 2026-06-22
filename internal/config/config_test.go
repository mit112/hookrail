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
