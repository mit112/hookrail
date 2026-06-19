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
