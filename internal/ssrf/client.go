package ssrf

import (
	"net/http"
	"time"
)

// MaxResponseBytes caps how much of a consumer's response we read (§8).
const MaxResponseBytes = 64 * 1024

// NewHTTPClient builds the hardened delivery client: pinning dialer,
// redirects disabled, split timeout budget (connect 3s in Dialer / TLS 3s /
// response headers 10s / total 15s — §8).
func NewHTTPClient(p Policy) *http.Client {
	d := &Dialer{Policy: p}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext:           d.DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ForceAttemptHTTP2:     true,
			MaxIdleConnsPerHost:   8,
			DisableKeepAlives:     false,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // never follow (§7, §8)
		},
	}
}
