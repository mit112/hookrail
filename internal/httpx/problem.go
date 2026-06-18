// Package httpx holds HTTP helpers shared by the api and admin servers:
// RFC-7807 problem responses and opaque keyset-pagination cursors (design §2.1).
package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

// Problem writes an RFC 7807 problem+json response (master spec §9).
func Problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}

// ClampLimit parses a ?limit= value, applying default and hard max (design §2.1).
// Empty, non-numeric, zero, or negative values fall back to def.
func ClampLimit(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// EncodeCursor renders an immutable sort key as an opaque, unsigned cursor.
func EncodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// DecodeCursor reverses EncodeCursor. Empty input means "from the start".
// A non-decodable cursor is an error the caller maps to HTTP 400 (design §2.1).
func DecodeCursor(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", errors.New("httpx: malformed cursor")
	}
	return string(b), nil
}
