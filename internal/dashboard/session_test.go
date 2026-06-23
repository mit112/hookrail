package dashboard

import (
	"encoding/json"
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

func TestSessionRoundTripCarriesSub(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now, "alice")
	sub, ok := s.Valid(v, now)
	if !ok || sub != "alice" {
		t.Fatalf("freshly issued cookie: ok=%v sub=%q", ok, sub)
	}
}

func TestSessionExpired(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now, "alice")
	if _, ok := s.Valid(v, now.Add(2*time.Hour)); ok {
		t.Fatal("expired cookie must be rejected")
	}
}

func TestSessionTampered(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := s.Issue(now, "alice")
	parts := strings.SplitN(v, ".", 2)
	if _, ok := s.Valid(parts[0]+".AAAA", now); ok {
		t.Fatal("tampered tag must be rejected")
	}
	if _, ok := s.Valid("eyJhIjoxfQ."+parts[1], now); ok {
		t.Fatal("tampered payload must be rejected")
	}
}

func TestSessionRejectsV1NoSub(t *testing.T) {
	s := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	// Forge a v1-style payload (no sub) signed with the real key.
	v1 := sessionPayload{V: 1, Kid: 0, Iat: now.Unix(), Exp: now.Add(time.Hour).Unix()}
	b, _ := json.Marshal(v1)
	tok := s.sign(b, 0)
	if _, ok := s.Valid(tok, now); ok {
		t.Fatal("v1 cookie (no sub) must be rejected")
	}
}

func TestSessionPreviousKeyVerifies(t *testing.T) {
	old := testSessions(nil)
	now := time.Unix(1_700_000_000, 0)
	v := old.Issue(now, "alice")
	rotated := NewSessions(Config{
		SessionKey:  []byte("ffffffffffffffffffffffffffffffff"),
		SessionPrev: []byte("0123456789abcdef0123456789abcdef"),
		SessionTTL:  time.Hour,
	})
	if sub, ok := rotated.Valid(v, now); !ok || sub != "alice" {
		t.Fatalf("cookie signed by previous key must still verify: ok=%v sub=%q", ok, sub)
	}
}
