package ratelimit

import (
	"testing"
	"time"
)

func TestBucketBurstThenDeny(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	b := NewBucket(10, 5) // 10 tokens/s, burst 5
	for i := 0; i < 5; i++ {
		if !b.Allow(now) {
			t.Fatalf("request %d within burst denied", i)
		}
	}
	if b.Allow(now) {
		t.Fatal("request beyond burst allowed at t=0")
	}
}

func TestBucketRefills(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	b := NewBucket(10, 5)
	for i := 0; i < 5; i++ {
		b.Allow(now)
	}
	// 10/s → one token back every 100ms
	if b.Allow(now.Add(50 * time.Millisecond)) {
		t.Fatal("allowed before a full token refilled")
	}
	if !b.Allow(now.Add(150 * time.Millisecond)) {
		t.Fatal("denied after a token refilled")
	}
}

func TestBucketCapsAtBurst(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	b := NewBucket(10, 5)
	// long idle must not bank more than burst
	later := now.Add(time.Hour)
	for i := 0; i < 5; i++ {
		if !b.Allow(later) {
			t.Fatalf("request %d after idle denied", i)
		}
	}
	if b.Allow(later) {
		t.Fatal("bucket banked more than burst over idle")
	}
}

func TestRegistryIsolatesKeys(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	r := NewRegistry(1, 1) // default 1 rps, burst 1
	if !r.Allow("ep-a", now) {
		t.Fatal("first ep-a denied")
	}
	if r.Allow("ep-a", now) {
		t.Fatal("second ep-a allowed")
	}
	if !r.Allow("ep-b", now) {
		t.Fatal("ep-b throttled by ep-a's bucket")
	}
}
