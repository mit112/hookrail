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
)

// validNext maps each state to the set of states it may move to.
// dead_lettered -> pending is the manual-replay path only.
var validNext = map[State]map[State]bool{
	StatePending:        {StateInFlight: true},
	StateRetryScheduled: {StateInFlight: true},
	StateInFlight:       {StateSucceeded: true, StateRetryScheduled: true, StateDeadLettered: true},
	StateDeadLettered:   {StatePending: true},
	StateSucceeded:      {},
}

func CanTransition(from, to State) bool {
	return validNext[from][to]
}

// IsTerminalForWorker reports whether workers must never touch this
// delivery again. dead_lettered is excluded: it has a manual replay exit.
func (s State) IsTerminalForWorker() bool {
	return s == StateSucceeded
}
