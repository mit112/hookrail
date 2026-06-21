package domain

import "testing"

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		// legal transitions (§6)
		{StatePending, StateInFlight, true},
		{StateRetryScheduled, StateInFlight, true},
		{StateInFlight, StateSucceeded, true},
		{StateInFlight, StateRetryScheduled, true},
		{StateInFlight, StateDeadLettered, true},
		{StateDeadLettered, StatePending, true}, // manual replay
		// illegal transitions
		{StatePending, StateSucceeded, false},
		{StatePending, StateRetryScheduled, false},
		{StatePending, StateDeadLettered, false},
		{StateRetryScheduled, StateSucceeded, false},
		{StateRetryScheduled, StateDeadLettered, false},
		{StateInFlight, StatePending, false},
		{StateSucceeded, StateInFlight, false},   // terminal
		{StateSucceeded, StatePending, false},    // terminal
		{StateDeadLettered, StateInFlight, false},
		{StatePending, StatePending, false},      // no self-loops
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	cases := []struct {
		s    State
		want bool
	}{
		{StateSucceeded, true},
		{StateDeadLettered, false}, // replayable, so not strictly terminal — but no automatic exit
		{StatePending, false},
		{StateInFlight, false},
		{StateRetryScheduled, false},
	}
	for _, c := range cases {
		if got := c.s.IsTerminalForWorker(); got != c.want {
			t.Errorf("IsTerminalForWorker(%s) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestOrderedTerminalSets(t *testing.T) {
	for _, s := range []State{StateSucceeded, StateSkipped, StateCancelled} {
		if Blocks(s) {
			t.Fatalf("%s must not block a key", s)
		}
	}
	for _, s := range []State{StatePending, StateInFlight, StateRetryScheduled, StateDeadLettered} {
		if !Blocks(s) {
			t.Fatalf("%s must block a key", s)
		}
	}
}
