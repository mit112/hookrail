// Package backoff implements exponential backoff with full jitter and
// Retry-After interaction per spec §4: delay = uniform[0, min(cap, base·2^(n-1))];
// a Retry-After larger than the drawn delay wins, capped at Cap.
package backoff

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	Base        time.Duration
	Cap         time.Duration
	MaxAttempts int
}

func Default() Policy {
	return Policy{Base: 5 * time.Second, Cap: 6 * time.Hour, MaxAttempts: 8}
}

// NextDelay returns the delay to schedule after attempt number `attempt`
// (1-based) failed retryably. retryAfter <= 0 means no Retry-After header.
func (p Policy) NextDelay(attempt int, retryAfter time.Duration, rnd *rand.Rand) time.Duration {
	ceil := p.Cap
	// base << (attempt-1) overflows for large attempts; only shift while safe.
	if attempt >= 1 && attempt-1 < 40 {
		if shifted := p.Base << (attempt - 1); shifted > 0 && shifted < p.Cap {
			ceil = shifted
		}
	}
	d := time.Duration(rnd.Int63n(int64(ceil) + 1)) // full jitter: [0, ceil]
	if retryAfter > d {
		d = retryAfter
		if d > p.Cap {
			d = p.Cap
		}
	}
	return d
}

// ParseRetryAfter parses an RFC 9110 Retry-After value: delta-seconds or
// HTTP-date. Returns (0, false) for absent/invalid/past values.
func ParseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
	}
	return 0, false
}
