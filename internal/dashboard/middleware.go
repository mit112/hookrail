package dashboard

import (
	"net/http"
	"net/url"
	"strings"
)

// sameOrigin reports whether the request's Origin header (if present) matches
// the host this server is serving. It compares only the normalized HOSTNAME,
// not the scheme or port: behind a TLS-terminating reverse proxy (Tailscale
// Funnel, Cloudflare Tunnel) the edge speaks HTTPS to the browser but plain
// HTTP to this process, so r.TLS is nil and the browser-visible port (443)
// differs from what the backend sees — a scheme/port-sensitive check would
// reject every legitimate same-origin mutating request (e.g. POST /api/logout).
// SameSite=Strict cookies are the primary CSRF defense; this host check is
// defense in depth.
//
// A missing Origin header is treated as same-origin: browsers omit it for
// same-origin GETs and some same-origin form posts, and non-browser clients
// (curl, SDKs) that carry the session cookie are not subject to CSRF. An
// opaque/"null" Origin (u.Host == "") is rejected.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	ru := &url.URL{Host: r.Host}
	if normHost(u.Hostname()) != normHost(ru.Hostname()) {
		return false
	}
	return portsCompatible(u.Port(), ru.Port())
}

// normHost folds case and strips a trailing dot so equivalent hostnames compare
// equal. url.Hostname() has already removed any port and IPv6 brackets.
func normHost(h string) string {
	return strings.TrimSuffix(strings.ToLower(h), ".")
}

// portsCompatible reports whether two host ports name the same origin. Behind a
// TLS-terminating edge the legitimate browser Origin carries no explicit port
// (default 443 omitted) while the backend's r.Host may be bare or carry a
// default port, so "", "80", and "443" are treated as equivalent. A non-default
// explicit port must match exactly — https://host:8080 is NOT same-origin as
// bare host (a genuinely different origin, not just a proxy artifact).
func portsCompatible(a, b string) bool {
	if a == b {
		return true
	}
	isDefault := func(p string) bool { return p == "" || p == "80" || p == "443" }
	return isDefault(a) && isDefault(b)
}
