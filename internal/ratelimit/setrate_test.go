package ratelimit

import (
	"testing"
	"time"
)

func TestRegistrySetRateReconfigures(t *testing.T) {
	r := NewRegistry(1000, 2000)
	key := "ep1"
	// drain the default bucket quickly is unnecessary; just reconfigure to a
	// very low rate and confirm the next bucket honors it.
	r.SetRate(key, 0.0001, 1) // ~1 token, then starved
	now := time.Now()
	if !r.Allow(key, now) {
		t.Fatal("first token should be allowed (burst=1)")
	}
	if r.Allow(key, now) {
		t.Fatal("second immediate call must be denied at the reconfigured low rate")
	}
}
