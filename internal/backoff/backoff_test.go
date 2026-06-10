package backoff

import (
	"math/rand"
	"testing"
	"time"
)

func TestNextDelayFullJitterBounds(t *testing.T) {
	p := Default()
	rnd := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic jitter, not crypto
	for attempt := 1; attempt <= 8; attempt++ {
		ceil := p.Base << (attempt - 1)
		if ceil > p.Cap {
			ceil = p.Cap
		}
		for i := 0; i < 1000; i++ {
			d := p.NextDelay(attempt, 0, rnd)
			if d < 0 || d > ceil {
				t.Fatalf("attempt %d: delay %v outside [0, %v]", attempt, d, ceil)
			}
		}
	}
}

func TestNextDelayNoOverflowAtHighAttempt(t *testing.T) {
	p := Default()
	rnd := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic jitter, not crypto
	for i := 0; i < 100; i++ {
		d := p.NextDelay(64, 0, rnd)
		if d < 0 || d > p.Cap {
			t.Fatalf("attempt 64: delay %v outside [0, %v]", d, p.Cap)
		}
	}
}

func TestRetryAfterOverridesWhenLarger(t *testing.T) {
	p := Default()
	rnd := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic jitter, not crypto
	if d := p.NextDelay(1, 10*time.Minute, rnd); d != 10*time.Minute {
		t.Errorf("NextDelay(1, 10m) = %v, want 10m", d)
	}
	if d := p.NextDelay(1, 24*time.Hour, rnd); d != p.Cap {
		t.Errorf("NextDelay(1, 24h) = %v, want cap %v", d, p.Cap)
	}
}

func TestRetryAfterIgnoredWhenSmaller(t *testing.T) {
	p := Policy{Base: time.Hour, Cap: 6 * time.Hour, MaxAttempts: 8}
	for i := 0; i < 200; i++ {
		withRA := p.NextDelay(3, time.Nanosecond, rand.New(rand.NewSource(int64(i)))) //nolint:gosec
		without := p.NextDelay(3, 0, rand.New(rand.NewSource(int64(i))))           //nolint:gosec
		if withRA != without {
			t.Fatalf("seed %d: tiny Retry-After changed delay: %v vs %v", i, withRA, without)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"120", 120 * time.Second, true},
		{" 0", 0, true},
		{"-5", 0, false},
		{"", 0, false},
		{"garbage", 0, false},
		{"Wed, 10 Jun 2026 12:05:00 GMT", 5 * time.Minute, true},
		{"Wed, 10 Jun 2026 11:00:00 GMT", 0, false},
	}
	for _, c := range cases {
		got, ok := ParseRetryAfter(c.in, now)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseRetryAfter(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
