// cmd/hookrail-ctl/help_test.go
package main_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCtlHelpHasNoSideEffects builds the binary and runs help variants with an
// EMPTY environment. config.Load() would fail (no DATABASE_URL) and exit 1 if
// reached, so a clean exit 0 proves help short-circuits before any side effect.
func TestCtlHelpHasNoSideEffects(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "hookrail-ctl")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil { //nolint:gosec
		t.Fatalf("build: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"},
		{"migrate", "--help"}, {"seed", "--help"}, {"retention", "--help"},
	} {
		cmd := exec.Command(bin, args...) //nolint:gosec
		cmd.Env = []string{} // no env at all: any config.Load/store.Open would fail nonzero
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ctl %v: help must exit 0 with no env, got %v\n%s", args, err, out)
		}
	}
}
