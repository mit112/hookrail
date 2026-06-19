package dashboard

import (
	"strings"
	"testing"
	"time"
)

func testSessions(prev []byte) *Sessions {
	return NewSessions(Config{
		SessionKey:  []byte("0123456789abcdef0123456789abcdef"),
		SessionPrev: prev,
		SessionTTL:  time.Hour,
	})
}

func TestSessionRoundTrip(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	if !s.Valid(v, now) {
		t.Fatal("freshly issued cookie should be valid")
	}
}

func TestSessionExpired(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	if s.Valid(v, now.Add(2*time.Hour)) {
		t.Fatal("expired cookie must be rejected")
	}
}

func TestSessionTampered(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now)
	parts := strings.SplitN(v, ".", 2)
	if s.Valid(parts[0]+".AAAA", now) {
		t.Fatal("tampered tag must be rejected")
	}
	if s.Valid("eyJhIjoxfQ."+parts[1], now) {
		t.Fatal("tampered payload must be rejected")
	}
}

func TestSessionPreviousKeyVerifies(t *testing.T) {
	old := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := old.Issue(now)
	rotated := NewSessions(Config{
		SessionKey:  []byte("ffffffffffffffffffffffffffffffff"),
		SessionPrev: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:  time.Hour,
	})
	if !rotated.Valid(v, now) {
		t.Fatal("cookie signed by previous key must still verify")
	}
}
