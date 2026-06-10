// Package ssrf implements the §8 defense chain: URL policy at registration
// and at dial, blocked IP ranges, resolve-then-pin dialing (defeats DNS
// rebinding), no redirects, capped response reads, split timeout budget.
package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"

	"github.com/mit112/hookrail/internal/domain"
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

type Policy struct {
	// AllowHTTP permits plain http URLs (local/self-host dev only; §8).
	AllowHTTP bool
	// AllowCIDRs is the self-hoster allowlist: ranges here bypass the
	// blocked-range check (delivering to internal networks; §8).
	AllowCIDRs []netip.Prefix
}

// CheckIP rejects loopback, RFC1918, link-local (incl. 169.254/16 cloud
// metadata), CGNAT, ULA/IPv6-local, unspecified, multicast and broadcast.
func (p Policy) CheckIP(ip netip.Addr) error {
	ip = ip.Unmap() // ::ffff:a.b.c.d must be judged as IPv4
	for _, pfx := range p.AllowCIDRs {
		if pfx.Contains(ip) {
			return nil
		}
	}
	blocked := ip.IsLoopback() ||
		ip.IsPrivate() || // RFC1918 + ULA fc00::/7
		ip.IsLinkLocalUnicast() || // 169.254/16 + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		cgnat.Contains(ip) ||
		ip == netip.MustParseAddr("255.255.255.255")
	if blocked {
		return fmt.Errorf("%w: ip %s is in a blocked range", domain.ErrPolicyViolation, ip)
	}
	return nil
}

// ValidateURL enforces scheme/host policy. Called at endpoint registration
// AND at every dial (§8: "DNS changes between").
func (p Policy) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: unparseable url: %v", domain.ErrPolicyViolation, err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowHTTP {
			return fmt.Errorf("%w: plain http requires --allow-http (dev only)", domain.ErrPolicyViolation)
		}
	default:
		return fmt.Errorf("%w: scheme %q not allowed", domain.ErrPolicyViolation, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: empty host", domain.ErrPolicyViolation)
	}
	if u.User != nil {
		return fmt.Errorf("%w: userinfo in url not allowed", domain.ErrPolicyViolation)
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return p.CheckIP(ip)
	}
	return nil
}

// CheckURLResolved is the REGISTRATION-time check (§5: "validated at write
// and at dial"): ValidateURL plus a best-effort DNS resolution of hostname
// endpoints, so a hostname already pointing at a blocked range is rejected
// at write time. Advisory — DNS can change after registration, so the
// dialer's at-dial check stays authoritative; an unresolvable host passes
// here (it will fail at dial, retryably).
func (p Policy) CheckURLResolved(ctx context.Context, raw string) error {
	if err := p.ValidateURL(raw); err != nil {
		return err
	}
	u, _ := url.Parse(raw) // ValidateURL already proved it parses
	host := u.Hostname()
	if _, err := netip.ParseAddr(host); err == nil {
		return nil // IP literal: already checked by ValidateURL
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil
	}
	for _, ip := range addrs {
		if err := p.CheckIP(ip); err != nil {
			return err
		}
	}
	return nil
}
