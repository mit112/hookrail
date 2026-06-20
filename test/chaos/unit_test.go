package chaos

import (
	"context"
	"reflect"
	"testing"
)

func TestInjectorIssuesCorrectDockerArgs(t *testing.T) {
	var got []string
	inj := &Injector{
		C:   &Compose{File: "f.yml", Project: "hookrail"},
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) { got = append([]string{name}, args...); return nil, nil },
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
		{"clean", Snapshot{Stats: Stats{Receipts: 10, Distinct: 10, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedDeliveries: 10, DupBound: 2}, true},
		{"dups within bound", Snapshot{Stats: Stats{Receipts: 12, Distinct: 10, Duplicates: 2}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedDeliveries: 10, DupBound: 2}, true},
		{"stranded in_flight", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, InFlight: 1}, ExpectedDeliveries: 10, DupBound: 2}, false},
		{"stranded retry_scheduled", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, RetryScheduled: 1}, ExpectedDeliveries: 10, DupBound: 2}, false},
		{"missing delivery", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedDeliveries: 10, DupBound: 2}, false},
		{"dup storm over bound", Snapshot{Stats: Stats{Receipts: 20, Distinct: 10, Duplicates: 10}, DB: DBState{Total: 10, Succeeded: 10}, ExpectedDeliveries: 10, DupBound: 2}, false},
		{"unexpected dead-letter", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, DeadLettered: 1}, ExpectedDeliveries: 10, DupBound: 2}, false},
		{"unexpected cancelled", Snapshot{Stats: Stats{Receipts: 9, Distinct: 9, Duplicates: 0}, DB: DBState{Total: 10, Succeeded: 9, Cancelled: 1}, ExpectedDeliveries: 10, DupBound: 2}, false},
	}
	for _, tc := range cases {
		if got := tc.s.Recovered(); got != tc.want {
			t.Errorf("%s: Recovered()=%v want %v (nonTerminal=%d)", tc.name, got, tc.want, tc.s.DB.NonTerminal())
		}
	}
}
