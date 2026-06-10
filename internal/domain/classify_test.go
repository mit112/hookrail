package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyStatus(t *testing.T) {
	cases := []struct {
		status   int
		outcome  Outcome
		errClass string
	}{
		{200, OutcomeSuccess, ""},
		{201, OutcomeSuccess, ""},
		{204, OutcomeSuccess, ""},
		{408, OutcomeRetryable, "http_408"},
		{425, OutcomeRetryable, "http_425"},
		{429, OutcomeRetryable, "http_429"},
		{400, OutcomePermanent, "http_400"},
		{401, OutcomePermanent, "http_401"},
		{404, OutcomePermanent, "http_404"},
		{410, OutcomePermanent, "http_410"},
		{422, OutcomePermanent, "http_422"},
		{500, OutcomeRetryable, "http_500"},
		{502, OutcomeRetryable, "http_502"},
		{503, OutcomeRetryable, "http_503"},
		{301, OutcomePermanent, "redirect_rejected"},
		{302, OutcomePermanent, "redirect_rejected"},
		{307, OutcomePermanent, "redirect_rejected"},
	}
	for _, c := range cases {
		o, ec := ClassifyStatus(c.status)
		if o != c.outcome || ec != c.errClass {
			t.Errorf("ClassifyStatus(%d) = (%v, %q), want (%v, %q)",
				c.status, o, ec, c.outcome, c.errClass)
		}
	}
}

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		outcome  Outcome
		errClass string
	}{
		{"deadline", context.DeadlineExceeded, OutcomeRetryable, "timeout"},
		{"wrapped deadline", fmt.Errorf("do: %w", context.DeadlineExceeded), OutcomeRetryable, "timeout"},
		{"ssrf policy", fmt.Errorf("dial: %w", ErrPolicyViolation), OutcomePermanent, "policy"},
		{"plain conn refused", errors.New("connect: connection refused"), OutcomeRetryable, "connection"},
		{"panic sentinel", ErrPanic, OutcomePermanent, "panic"},
	}
	for _, c := range cases {
		o, ec := ClassifyError(c.err)
		if o != c.outcome || ec != c.errClass {
			t.Errorf("%s: ClassifyError = (%v, %q), want (%v, %q)",
				c.name, o, ec, c.outcome, c.errClass)
		}
	}
}
