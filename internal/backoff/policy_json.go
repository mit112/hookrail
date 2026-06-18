// internal/backoff/policy_json.go
package backoff

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// MaxPolicyMS bounds base_ms/cap_ms so the resolved Cap can never overflow
// int64 nanoseconds NOR make NextDelay's `int64(ceil)+1` wrap negative (which
// panics rand.Int63n — see backoff.go). 7 days in ms: far above any sane
// webhook backoff, far below the int64-nanosecond overflow edge.
const MaxPolicyMS int64 = 7 * 24 * 60 * 60 * 1000 // 604_800_000

type policyJSON struct {
	BaseMS *int64 `json:"base_ms"`
	CapMS  *int64 `json:"cap_ms"`
}

// Validate is the write-time check (handler → 422). nil/empty is valid (default).
// Rejects malformed JSON, UNKNOWN FIELDS, missing/non-positive values, values
// above MaxPolicyMS, and cap < base — all checked on the int64 BEFORE any
// time.Duration multiplication (so a hostile value can't overflow during checks).
func Validate(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p policyJSON
	if err := dec.Decode(&p); err != nil {
		return errors.New("backoff_policy: malformed JSON or unknown field")
	}
	// Reject trailing data: json.Decoder.Decode reads one value and ignores the
	// rest, so `{...} garbage` would otherwise pass here and only fail later as
	// invalid JSONB on INSERT — surfacing as a 503 instead of the required 422.
	if dec.More() {
		return errors.New("backoff_policy: unexpected trailing data after the JSON object")
	}
	if p.BaseMS == nil || p.CapMS == nil {
		return errors.New("backoff_policy: base_ms and cap_ms are required")
	}
	if *p.BaseMS <= 0 || *p.CapMS <= 0 {
		return errors.New("backoff_policy: base_ms and cap_ms must be > 0")
	}
	if *p.BaseMS > MaxPolicyMS || *p.CapMS > MaxPolicyMS {
		return errors.New("backoff_policy: base_ms and cap_ms must be <= 604800000 (7 days)")
	}
	if *p.CapMS < *p.BaseMS {
		return errors.New("backoff_policy: cap_ms must be >= base_ms")
	}
	return nil
}

// FromJSON is the read path. It NEVER errors and NEVER yields a policy that can
// overflow/panic NextDelay: base/cap are forced into (0, MaxPolicyMS]. The
// clamp is on the int64 ms BEFORE multiplication, so even math.MaxInt64 input
// is safe. nil/empty/invalid → Default with the given maxAttempts.
func FromJSON(raw []byte, maxAttempts int) Policy {
	d := Default()
	if maxAttempts > 0 {
		d.MaxAttempts = maxAttempts
	}
	if len(raw) == 0 {
		return d
	}
	var p policyJSON
	if err := json.Unmarshal(raw, &p); err != nil || p.BaseMS == nil || p.CapMS == nil {
		return d
	}
	clamp := func(ms int64, def time.Duration) time.Duration {
		if ms <= 0 || ms > MaxPolicyMS { // reject before multiplying
			return def
		}
		return time.Duration(ms) * time.Millisecond
	}
	base := clamp(*p.BaseMS, d.Base)
	cp := clamp(*p.CapMS, d.Cap)
	if cp < base {
		cp = base
	}
	return Policy{Base: base, Cap: cp, MaxAttempts: d.MaxAttempts}
}
