package redisclient

import (
	"testing"
	"time"
)

func TestNew_PlainMode_BareAddr(t *testing.T) {
	c, err := New(Options{Addr: "localhost:6379"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	if got := c.Options().Addr; got != "localhost:6379" {
		t.Fatalf("addr=%q want localhost:6379", got)
	}
}

func TestNew_PlainMode_URL(t *testing.T) {
	c, err := New(Options{Addr: "redis://h:6380/2"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	if c.Options().DB != 2 {
		t.Fatalf("db=%d want 2", c.Options().DB)
	}
}

func TestNew_SentinelMode_WinsOverAddr(t *testing.T) {
	o := Options{Addr: "ignored:6379", SentinelAddrs: []string{"s1:26379", "s2:26379"}, MasterName: "hookrail"}
	if !o.Sentinel() {
		t.Fatal("Sentinel() should be true when SentinelAddrs non-empty")
	}
	c, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	if c.Options().Addr == "ignored:6379" {
		t.Fatal("sentinel-mode client must not use the plain Addr")
	}
}

func TestNew_SentinelMode_RequiresMasterName(t *testing.T) {
	_, err := New(Options{SentinelAddrs: []string{"s1:26379"}})
	if err == nil {
		t.Fatal("expected error when MasterName empty in sentinel mode")
	}
}

func TestNew_AppliesTimeouts(t *testing.T) {
	c, err := New(Options{Addr: "x:6379", PoolSize: 8, ReadTimeout: 200 * time.Millisecond, WriteTimeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	if c.Options().PoolSize != 8 || c.Options().ReadTimeout != 200*time.Millisecond {
		t.Fatalf("opts not applied: pool=%d read=%s", c.Options().PoolSize, c.Options().ReadTimeout)
	}
}
