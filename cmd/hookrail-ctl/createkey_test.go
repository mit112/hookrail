package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUsageMentionsCreateProducerKey(t *testing.T) {
	out, _ := exec.Command("go", "run", ".", "-h").CombinedOutput()
	if !strings.Contains(string(out), "create-producer-key") {
		t.Fatalf("usage did not mention create-producer-key: %s", out)
	}
}

func TestUsageMentionsScope(t *testing.T) {
	out, _ := exec.Command("go", "run", ".", "-h").CombinedOutput()
	if !strings.Contains(string(out), "-scope") {
		t.Fatalf("usage did not mention -scope: %s", out)
	}
}

// A create-producer-key with no -scope is refused BEFORE any DB access (the
// len(scopes)==0 check precedes config.Load), so this needs no Postgres.
func TestCreateProducerKeyRequiresScopeFlag(t *testing.T) {
	out, err := exec.Command("go", "run", ".", "create-producer-key", "-name", "x").CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit with no -scope, got success: %s", out)
	}
}
