package store_test

import (
	"testing"

	"github.com/mit112/hookrail/internal/store"
)

func TestValidateTopicPattern(t *testing.T) {
	valid := []string{"*", "orders", "orders.*", "payments.captured", "a.b.c.*"}
	for _, p := range valid {
		if err := store.ValidateTopicPattern(p); err != nil {
			t.Errorf("ValidateTopicPattern(%q) = %v, want nil", p, err)
		}
	}
	invalid := []string{"", "  ", "has space", "tab\tinside", "new\nline"}
	for _, p := range invalid {
		if err := store.ValidateTopicPattern(p); err == nil {
			t.Errorf("ValidateTopicPattern(%q) = nil, want error", p)
		}
	}
	// length cap (>255)
	long := make([]byte, 256)
	for i := range long {
		long[i] = 'a'
	}
	if err := store.ValidateTopicPattern(string(long)); err == nil {
		t.Error("ValidateTopicPattern(256 chars) = nil, want error")
	}
}
