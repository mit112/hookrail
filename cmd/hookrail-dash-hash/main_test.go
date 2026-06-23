package main

import (
	"strings"
	"testing"

	"github.com/mit112/hookrail/internal/dashboard"
)

func TestEmitLine(t *testing.T) {
	h, err := dashboard.HashPassword("pw")
	if err != nil {
		t.Fatal(err)
	}
	got := emitLine("alice", h, "admin")
	if !strings.HasPrefix(got, "alice:$argon2id$") || !strings.HasSuffix(got, ":admin") {
		t.Fatalf("bad line: %q", got)
	}
}
