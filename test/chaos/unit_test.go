//go:build chaos

package chaos

import (
	"context"
	"reflect"
	"testing"
)

func TestInjectorIssuesCorrectDockerArgs(t *testing.T) {
	var got []string
	inj := &Injector{
		C: &Compose{File: "f.yml", Project: "hookrail"},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			got = append([]string{name}, args...)
			return nil, nil
		},
	}
	cases := []struct {
		call func() error
		want []string
	}{
		{func() error { return inj.Kill(context.Background(), "worker") }, []string{"docker", "compose", "-f", "f.yml", "-p", "hookrail", "kill", "worker"}},
		{func() error { return inj.Pause(context.Background(), "postgres") }, []string{"docker", "compose", "-f", "f.yml", "-p", "hookrail", "pause", "postgres"}},
		{func() error { return inj.Unpause(context.Background(), "postgres") }, []string{"docker", "compose", "-f", "f.yml", "-p", "hookrail", "unpause", "postgres"}},
		{func() error { return inj.Start(context.Background(), "worker") }, []string{"docker", "compose", "-f", "f.yml", "-p", "hookrail", "start", "worker"}},
	}
	for _, tc := range cases {
		if err := tc.call(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("args = %v, want %v", got, tc.want)
		}
	}
}

func TestRecoveredInvariant(t *testing.T) {
	cases := []struct {
		name string
		s    Snapshot
		want bool
	}{
		// Delivery-path faults (E1/E3): exact bound, Min==Max.
		{"clean", Snapshot{Stats: Stats{Receipts: 10, Distinct: 10, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, true},
		{"dups within bound", Snapshot{Stats: Stats{Receipts: 12, Distinct: 10, Duplicates: 2}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, true},
		{"stranded in_flight", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, InFlight: 1}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		{"stranded retry_scheduled", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, RetryScheduled: 1}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		{"missing delivery", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		{"dup storm over bound", Snapshot{Stats: Stats{Receipts: 20, Distinct: 10, Duplicates: 10}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		{"unexpected dead-letter", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, DeadLettered: 1}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		{"unexpected cancelled", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, Cancelled: 1}, ExpectedMin: 10, ExpectedMax: 10, DupBound: 2}, false},
		// Ingress-path fault (E2): succeeded may exceed accepted up to attempts (boundary
		// posts that commit on unpause). Min=accepted, Max=attempts+1.
		{"e2 boundary commit within bound", Snapshot{Stats: Stats{Receipts: 13, Distinct: 13, Duplicates: 0}, DB: DBState{Total: 13, Succeeded: 13}, ExpectedMin: 12, ExpectedMax: 14, DupBound: 10}, true},
		{"e2 succeeded below min (lost an accepted)", Snapshot{Stats: Stats{Receipts: 11, Distinct: 11, Duplicates: 0}, DB: DBState{Total: 11, Succeeded: 11}, ExpectedMin: 12, ExpectedMax: 14, DupBound: 10}, false},
		{"e2 succeeded above max (phantom over-acceptance)", Snapshot{Stats: Stats{Receipts: 15, Distinct: 15, Duplicates: 0}, DB: DBState{Total: 15, Succeeded: 15}, ExpectedMin: 12, ExpectedMax: 14, DupBound: 10}, false},
		{"distinct exceeds succeeded (phantom receipt)", Snapshot{Stats: Stats{Receipts: 13, Distinct: 13, Duplicates: 0}, DB: DBState{Total: 12, Succeeded: 12}, ExpectedMin: 12, ExpectedMax: 14, DupBound: 10}, false},
	}
	for _, tc := range cases {
		if got := tc.s.Recovered(); got != tc.want {
			t.Errorf("%s: Recovered()=%v want %v (nonTerminal=%d)", tc.name, got, tc.want, tc.s.DB.NonTerminal())
		}
	}
}
