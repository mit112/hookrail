package api

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey string

const producerKeyCtx ctxKey = "producer_key_id"

// authProducer resolves "Authorization: Bearer hk_..." to a producer key id.
// Keys are stored hashed (§4); lookup is by SHA-256 of the presented key.
func (s *Server) authProducer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || raw == "" {
			problem(w, http.StatusUnauthorized, "missing credentials", "Authorization: Bearer <producer key> required")
			return
		}
		keyID, err := s.store.LookupProducerKey(r.Context(), raw)
		if err != nil {
			problem(w, http.StatusUnauthorized, "invalid credentials", "unknown or revoked producer key")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), producerKeyCtx, keyID)))
	}
}
