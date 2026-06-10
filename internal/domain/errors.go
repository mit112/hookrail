package domain

import "errors"

// ErrPolicyViolation marks SSRF/TLS policy failures (§7 row 6, §8).
// The ssrf package wraps it; classification treats it as permanent.
var ErrPolicyViolation = errors.New("delivery policy violation")

// ErrPanic marks a recovered worker panic (poison payload, §10).
var ErrPanic = errors.New("worker panic during delivery")
