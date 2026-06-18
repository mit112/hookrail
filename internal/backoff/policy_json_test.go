package backoff

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	good := []string{`{"base_ms":1000,"cap_ms":60000}`, `{"base_ms":5,"cap_ms":5}`}
	for _, g := range good {
		if err := Validate([]byte(g)); err != nil {
			t.Fatalf("Validate(%s) = %v, want nil", g, err)
		}
	}
	bad := []string{
		`{"base_ms":0,"cap_ms":1}`,                              // non-positive
		`{"base_ms":10,"cap_ms":5}`,                             // cap < base
		`{"base_ms":-1,"cap_ms":1}`,                             // negative
		`{nope`,                                                 // malformed
		`{"base_ms":1,"cap_ms":1,"max_attempts":3}`,             // unknown field
		fmt.Sprintf(`{"base_ms":1,"cap_ms":%d}`, math.MaxInt64), // above MaxPolicyMS
	}
	for _, b := range bad {
		if err := Validate([]byte(b)); err == nil {
			t.Fatalf("Validate(%s) = nil, want error", b)
		}
	}
	if err := Validate(nil); err != nil {
		t.Fatalf("nil policy must be valid (default): %v", err)
	}
}

func TestFromJSONSafeAndClamps(t *testing.T) {
	rnd := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic jitter, not crypto
	if got := FromJSON(nil, 8); got != Default() {
		t.Fatalf("nil -> %v, want Default", got)
	}
	p := FromJSON([]byte(`{"base_ms":2000,"cap_ms":120000}`), 5)
	if p.Base != 2*time.Second || p.Cap != 120*time.Second || p.MaxAttempts != 5 {
		t.Fatalf("parsed = %+v", p)
	}
	// hostile values can NEVER reach a panicking ceil: negative AND MaxInt64
	// are both clamped to safe defaults BEFORE the time.Duration multiply.
	for _, raw := range []string{
		`{"base_ms":-9,"cap_ms":-9}`,
		fmt.Sprintf(`{"base_ms":%d,"cap_ms":%d}`, math.MaxInt64, math.MaxInt64),
	} {
		got := FromJSON([]byte(raw), 3)
		if got.Base <= 0 || got.Cap <= 0 || got.Cap > time.Duration(MaxPolicyMS)*time.Millisecond {
			t.Fatalf("clamp failed for %s: %+v", raw, got)
		}
		for n := 1; n <= 45; n++ {
			_ = got.NextDelay(n, 0, rnd) // must not panic at any attempt number
		}
	}
}
