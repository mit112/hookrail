package domain

import "testing"

func TestCancelledTransitions(t *testing.T) {
	if !CanTransition(StatePending, StateCancelled) {
		t.Fatal("pending → cancelled must be allowed")
	}
	if !CanTransition(StateRetryScheduled, StateCancelled) {
		t.Fatal("retry_scheduled → cancelled must be allowed")
	}
	if CanTransition(StateInFlight, StateCancelled) {
		t.Fatal("in_flight → cancelled must NOT be allowed (let the attempt finish)")
	}
	if CanTransition(StateCancelled, StatePending) {
		t.Fatal("cancelled is terminal")
	}
	if !StateCancelled.IsTerminalForWorker() {
		t.Fatal("cancelled must be terminal for workers")
	}
	if !StateSucceeded.IsTerminalForWorker() {
		t.Fatal("succeeded must remain terminal for workers")
	}
}
