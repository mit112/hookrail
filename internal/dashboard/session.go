package dashboard

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type Sessions struct {
	keys [][]byte
	ttl  time.Duration
}

type sessionPayload struct {
	V   int    `json:"v"`
	Kid int    `json:"kid"`
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
	Iat int64  `json:"iat"`
}

func NewSessions(cfg Config) *Sessions {
	keys := [][]byte{cfg.SessionKey}
	if cfg.SessionPrev != nil {
		keys = append(keys, cfg.SessionPrev)
	}
	return &Sessions{keys: keys, ttl: cfg.SessionTTL}
}

func (s *Sessions) sign(payload []byte, kid int) string {
	mac := hmac.New(sha256.New, s.keys[kid])
	mac.Write(payload)
	tag := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(tag)
}

// Issue mints a v2 session carrying the authenticated subject (username) only.
// The role is never stored in the cookie; it is resolved per-request from the
// live user file (RBAC R2, D3).
func (s *Sessions) Issue(now time.Time, sub string) string {
	p := sessionPayload{V: 2, Kid: 0, Sub: sub, Iat: now.Unix(), Exp: now.Add(s.ttl).Unix()}
	b, _ := json.Marshal(p)
	return s.sign(b, 0)
}

// Valid returns the authenticated subject and true iff value is a well-formed,
// unexpired, correctly-signed v2 session. v1 cookies (no sub) are rejected.
func (s *Sessions) Valid(value string, now time.Time) (string, bool) {
	i := strings.LastIndexByte(value, '.')
	if i < 0 {
		return "", false
	}
	payloadB64, tagB64 := value[:i], value[i+1:]
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return "", false
	}
	gotTag, err := base64.RawURLEncoding.DecodeString(tagB64)
	if err != nil {
		return "", false
	}
	var p sessionPayload
	if json.Unmarshal(payload, &p) != nil {
		return "", false
	}
	if p.V < 2 || p.Sub == "" {
		return "", false
	}
	if !now.Before(time.Unix(p.Exp, 0)) {
		return "", false
	}
	if p.Kid < 0 || p.Kid >= len(s.keys) {
		return "", false
	}
	// Try the key indicated by kid first (fast path), then all others
	// to handle key rotation (old cookies have kid=0, but after rotation
	// the old key may be at a different index).
	order := make([]int, 0, len(s.keys))
	order = append(order, p.Kid)
	for i := range s.keys {
		if i != p.Kid {
			order = append(order, i)
		}
	}
	for _, kid := range order {
		mac := hmac.New(sha256.New, s.keys[kid])
		mac.Write(payload)
		if subtle.ConstantTimeCompare(gotTag, mac.Sum(nil)) == 1 {
			return p.Sub, true
		}
	}
	return "", false
}
