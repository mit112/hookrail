package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/mit112/hookrail/internal/worker"
)

func TestInFlightReserveFinalizeRemove(t *testing.T) {
	tr := worker.NewInFlight()
	tr.Reserve()
	tr.Finalize("d1", 7)
	held := tr.DrainSnapshot(context.Background())
	if len(held) != 1 || held[0].ID != "d1" || held[0].ClaimVersion != 7 {
		t.Fatalf("snapshot=%v", held)
	}
	tr.Remove("d1")
	if len(tr.DrainSnapshot(context.Background())) != 0 {
		t.Fatal("d1 should be gone")
	}
}

func TestInFlightDrainWaitsForInProgressClaim(t *testing.T) {
	tr := worker.NewInFlight()
	tr.Reserve() // a claim is mid-flight, no Finalize yet
	done := make(chan []worker.Held, 1)
	go func() { done <- tr.DrainSnapshot(context.Background()) }()
	select {
	case <-done:
		t.Fatal("DrainSnapshot must block while a claim is in progress")
	case <-time.After(100 * time.Millisecond):
	}
	tr.Finalize("d2", 3) // claim resolves
	held := <-done
	if len(held) != 1 || held[0].ID != "d2" {
		t.Fatalf("snapshot=%v", held)
	}
}
