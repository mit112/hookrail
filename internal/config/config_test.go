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
