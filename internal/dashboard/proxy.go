package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mit112/hookrail/internal/admin"
	"github.com/mit112/hookrail/internal/httpx"
)

const maxBody = 256 << 10 // 256 KB, matches ingest payload max

var safeReqHeaders = map[string]bool{"Content-Type": true}
var safeRespHeaders = map[string]bool{"Content-Type": true, "Cache-Control": true}

// proxyClient never follows redirects.
var proxyClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Timeout:       0, // per-request context timeout instead
}

// proxyAdmin builds a FRESH outbound request to the admin upstream, injecting the admin token.
func (s *Server) proxyAdmin(w http.ResponseWriter, r *http.Request) {
	// Reject absolute-form / suspicious request targets — only origin-form paths proxy.
	if r.URL.IsAbs() || r.URL.Host != "" || !strings.HasPrefix(r.URL.Path, "/v1/") {
		httpx.Problem(w, http.StatusBadRequest, "bad request target", "only origin-form /v1 paths are proxied")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	url := s.cfg.AdminURL + r.URL.Path
	if r.URL.RawQuery != "" {
		url += "?" + r.URL.RawQuery
	}
	// MaxBytesReader caps the request body; reading past the cap yields *http.MaxBytesError → 413.
	body := http.MaxBytesReader(w, r.Body, maxBody)
	// SSRF via AdminURL is by design — proxy targets are env-configured upstreams.
	//nolint:gosec
	out, err := http.NewRequestWithContext(ctx, r.Method, url, body)
	if err != nil {
		httpx.Problem(w, http.StatusBadGateway, "proxy error", "could not build upstream request")
		return
	}
	for k, v := range r.Header {
		if safeReqHeaders[http.CanonicalHeaderKey(k)] {
			out.Header[http.CanonicalHeaderKey(k)] = v
		}
	}
	out.Header.Set("Authorization", "Bearer "+s.cfg.AdminToken)
	// SSRF via AdminURL is by design — proxy targets are env-configured upstreams.
	//nolint:gosec
	resp, err := proxyClient.Do(out)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.Problem(w, http.StatusRequestEntityTooLarge, "body too large", "request body exceeds 256KB")
			return
		}
		httpx.Problem(w, http.StatusBadGateway, "upstream unreachable", "admin API did not respond")
		return
	}
	// Body is closed by defer — close error is harmless.
	//nolint:errcheck
	defer resp.Body.Close()
	for k, v := range resp.Header {
		if safeRespHeaders[http.CanonicalHeaderKey(k)] {
			w.Header()[http.CanonicalHeaderKey(k)] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, maxBody))
}

type route struct {
	method  string
	path    string // ServeMux pattern, e.g. "/v1/endpoints/{id}"
	minRole admin.Role
}

// adminRoutes is the proxied allowlist. minRole MUST mirror the upstream
// internal/admin registry; policy_test.go asserts no drift.
func (s *Server) adminRoutes() []route {
	return []route{
		{"GET", "/v1/endpoints", admin.RoleViewer},
		{"POST", "/v1/endpoints", admin.RoleAdmin},
		{"GET", "/v1/endpoints/{id}", admin.RoleViewer},
		{"PATCH", "/v1/endpoints/{id}", admin.RoleAdmin},
		{"DELETE", "/v1/endpoints/{id}", admin.RoleAdmin},
		{"POST", "/v1/endpoints/{id}/rotate-secret", admin.RoleAdmin},
		{"POST", "/v1/subscriptions", admin.RoleAdmin},
		{"GET", "/v1/subscriptions", admin.RoleViewer},
		{"GET", "/v1/subscriptions/{id}", admin.RoleViewer},
		{"PATCH", "/v1/subscriptions/{id}", admin.RoleAdmin},
		{"DELETE", "/v1/subscriptions/{id}", admin.RoleAdmin},
		{"GET", "/v1/dlq", admin.RoleViewer},
		{"POST", "/v1/dlq/{delivery_id}/replay", admin.RoleOperator},
		{"GET", "/v1/deliveries", admin.RoleViewer},
		{"GET", "/v1/deliveries/{id}", admin.RoleViewer},
	}
}
