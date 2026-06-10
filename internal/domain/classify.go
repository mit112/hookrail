package domain

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Outcome is the action class of spec §7.
type Outcome string

const (
	OutcomeSuccess   Outcome = "success"
	OutcomeRetryable Outcome = "retryable"
	OutcomePermanent Outcome = "permanent"
)

// ClassifyStatus implements the §7 table for responses that arrived.
func ClassifyStatus(status int) (Outcome, string) {
	switch {
	case status >= 200 && status < 300:
		return OutcomeSuccess, ""
	case status == 408 || status == 425 || status == 429:
		return OutcomeRetryable, fmt.Sprintf("http_%d", status)
	case status >= 300 && status < 400:
		return OutcomePermanent, "redirect_rejected"
	case status >= 400 && status < 500:
		return OutcomePermanent, fmt.Sprintf("http_%d", status)
	default: // 5xx and anything unexpected
		return OutcomeRetryable, fmt.Sprintf("http_%d", status)
	}
}

// ClassifyError implements §7 for requests that produced no response.
func ClassifyError(err error) (Outcome, string) {
	switch {
	case errors.Is(err, ErrPanic):
		return OutcomePermanent, "panic"
	case errors.Is(err, ErrPolicyViolation):
		return OutcomePermanent, "policy"
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeRetryable, "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return OutcomeRetryable, "timeout"
	}
	return OutcomeRetryable, "connection"
}
