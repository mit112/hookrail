package dashboard

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

func (s *Server) handleTestEvent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil || body.Topic == "" || !isJSONObject(body.Payload) {
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"topic": string, "payload": object}`)
		return
	}
	raw, _ := json.Marshal(map[string]any{"topic": body.Topic, "payload": body.Payload})
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// IngressURL is env-configured — SSRF by design.
	//nolint:gosec
	out, _ := http.NewRequestWithContext(ctx, "POST", s.cfg.IngressURL+"/v1/events", bytes.NewReader(raw))
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Authorization", "Bearer "+s.cfg.ProducerKey)
	out.Header.Set("Idempotency-Key", newIdempotencyKey())
	// IngressURL is env-configured — SSRF by design.
	//nolint:gosec
	resp, err := proxyClient.Do(out)
	if err != nil {
		httpx.Problem(w, http.StatusBadGateway, "upstream unreachable", "ingress did not respond")
		return
	}
	// Body close error is harmless in a proxy-style handler.
	//nolint:errcheck
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" { w.Header().Set("Content-Type", ct) }
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBody))
}

func isJSONObject(b json.RawMessage) bool {
	var m map[string]any
	return len(b) > 0 && json.Unmarshal(b, &m) == nil
}

func newIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "dash_" + hex.EncodeToString(b)
}
