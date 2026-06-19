//go:build integration

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// Uses the same Postgres the store itests use (HOOKRAIL_DATABASE_URL set by the
// itest harness / compose). Mirror how seed/migrate integration paths are exercised.
func TestCreateProducerKeyEmitsKey(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	out, err := exec.Command("go", "run", ".", "create-producer-key", "-name", "test").CombinedOutput()
	if err != nil {
		t.Fatalf("run failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "producer_key=hk_") {
		t.Fatalf("expected producer_key=hk_… in output, got: %s", out)
	}
}
