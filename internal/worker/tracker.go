package worker

import (
	"context"
	"sync"
)

// Held represents a delivery that has been claimed and is in-flight.
type Held struct {
	ID            string
	ClaimVersion  int64
}

// InFlight tracks deliveries that have been claimed but not yet completed.
// It provides a reserve-before-claim pattern to close the SIGTERM race:
// Reserve() is called before ClaimDelivery, and Finalize/Abort resolves it.
// DrainSnapshot blocks until all in-progress claims resolve, then returns
// a stable snapshot of held deliveries for graceful drain.
type InFlight struct {
	mu         sync.Mutex
	cond       *sync.Cond
	held       map[string]int64
	inProgress int
}

// NewInFlight creates a new InFlight tracker.
func NewInFlight() *InFlight {
	t := &InFlight{held: make(map[string]int64)}
	t.cond = sync.NewCond(&t.mu)
	return t
}

// Reserve increments the in-progress counter. Call before ClaimDelivery.
func (t *InFlight) Reserve() {
	t.mu.Lock()
	t.inProgress++
	t.mu.Unlock()
}

// Finalize records a successful claim: decrements inProgress and adds
// the delivery to the held map. Panics if inProgress == 0 (unbalanced).
func (t *InFlight) Finalize(id string, claimVersion int64) {
	t.mu.Lock()
	t.inProgress--
	if t.inProgress < 0 {
		t.mu.Unlock()
		panic("InFlight.Finalize: inProgress went negative (unbalanced Reserve/Finalize)")
	}
	t.held[id] = claimVersion
	t.cond.Broadcast()
	t.mu.Unlock()
}

// Abort decrements inProgress without adding to the held map.
// Use when ClaimDelivery returned no-row or an error (no claim was made).
func (t *InFlight) Abort() {
	t.mu.Lock()
	t.inProgress--
	if t.inProgress < 0 {
		t.mu.Unlock()
		panic("InFlight.Abort: inProgress went negative (unbalanced Reserve/Abort)")
	}
	t.cond.Broadcast()
	t.mu.Unlock()
}

// Remove deletes a delivery from the held map. Call after a confirmed
// terminal write (CompleteAttempt, DeadLetterExhausted, or ErrStaleClaim).
func (t *InFlight) Remove(id string) {
	t.mu.Lock()
	delete(t.held, id)
	t.mu.Unlock()
}

// DrainSnapshot blocks until inProgress == 0, then returns a stable copy
// of all held deliveries. If the context is canceled, it Broadasts the
// cond to unblock Wait and returns the current partial snapshot.
func (t *InFlight) DrainSnapshot(ctx context.Context) []Held {
	t.mu.Lock()
	defer t.mu.Unlock()

	// If the context supports cancellation, ensure cond.Wait unblocks
	// when it fires so we can return promptly.
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			t.cond.Broadcast()
		}()
	}

	for t.inProgress > 0 {
		t.cond.Wait()
		select {
		case <-ctx.Done():
			goto snapshot
		default:
		}
	}

snapshot:
	// Stable copy
	if len(t.held) == 0 {
		return nil
	}
	res := make([]Held, 0, len(t.held))
	for id, cv := range t.held {
		res = append(res, Held{ID: id, ClaimVersion: cv})
	}
	return res
}
