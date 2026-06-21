package domain

// State is the delivery state machine of spec §6. It lives in the
// deliveries table; every transition is committed in the same PG
// transaction as the attempt record that caused it.
type State string

const (
	StatePending        State = "pending"
	StateInFlight       State = "in_flight"
	StateRetryScheduled State = "retry_scheduled"
	StateSucceeded      State = "succeeded"
	StateDeadLettered   State = "dead_lettered"
	StateCancelled      State = "cancelled"
	StateSkipped        State = "skipped"
)

// validNext maps each state to the set of states it may move to.
// dead_lettered -> pending is the manual-replay path only.
var validNext = map[State]map[State]bool{
	StatePending:        {StateInFlight: true, StateCancelled: true},
	StateRetryScheduled: {StateInFlight: true, StateCancelled: true},
	StateInFlight:       {StateSucceeded: true, StateRetryScheduled: true, StateDeadLettered: true},
	StateDeadLettered:   {StatePending: true, StateSkipped: true},
	StateSucceeded:      {},
	StateCancelled:      {},
	StateSkipped:        {},
}

func CanTransition(from, to State) bool {
	return validNext[from][to]
}

// NonBlockingTerminal is the set of states that never block an ordering key.
// Cursor derivation skips rows in these states.
var NonBlockingTerminal = map[State]bool{
	StateSucceeded: true,
	StateSkipped:   true,
	StateCancelled: true,
}

// Blocks reports whether a delivery in this state blocks its ordering key
// (i.e. the key cannot deliver past this row until this row exits the key).
func Blocks(s State) bool {
	return !NonBlockingTerminal[s]
}

// IsTerminalForWorker reports whether workers must never touch this
// delivery again. dead_lettered is excluded: it has a manual replay exit.
func (s State) IsTerminalForWorker() bool {
	return s == StateSucceeded || s == StateCancelled || s == StateSkipped
}
