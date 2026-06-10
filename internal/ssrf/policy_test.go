package ssrf

import (
	"net/netip"
	"testing"

	"github.com/mit112/hookrail/internal/domain"
)

var domainClassify = domain.ClassifyError

func TestCheckIPBlockedRanges(t *testing.T) {
	p := Policy{} // default: nothing allowed beyond global unicast
	cases := []struct {
		ip      string
		blocked bool
		why     string
	}{
		{"127.0.0.1", true, "loopback"},
		{"127.8.8.8", true, "loopback /8"},
		{"10.1.2.3", true, "RFC1918 10/8"},
		{"172.16.0.1", true, "RFC1918 172.16/12"},
		{"172.31.255.255", true, "RFC1918 172.16/12 upper bound"},
		{"192.168.1.1", true, "RFC1918 192.168/16"},
		{"169.254.169.254", true, "link-local incl. cloud metadata (§8)"},
		{"169.254.0.1", true, "link-local"},
		{"100.64.0.1", true, "CGNAT 100.64/10"},
		{"100.127.255.255", true, "CGNAT upper bound"},
		{"0.0.0.0", true, "unspecified"},
		{"224.0.0.1", true, "multicast"},
		{"255.255.255.255", true, "broadcast"},
		{"::1", true, "IPv6 loopback"},
		{"fe80::1", true, "IPv6 link-local"},
		{"fc00::1", true, "ULA fc00::/7"},
		{"fd12:3456::1", true, "ULA fd00::/8"},
		{"::", true, "IPv6 unspecified"},
		{"::ffff:127.0.0.1", true, "v4-mapped loopback (Unmap before checking)"},
		{"::ffff:10.0.0.1", true, "v4-mapped RFC1918"},
		// allowed: real public addresses
		{"8.8.8.8", false, "public v4"},
		{"1.1.1.1", false, "public v4"},
		{"93.184.216.34", false, "public v4"},
		{"2606:4700:4700::1111", false, "public v6"},
	}
	for _, c := range cases {
		ip := netip.MustParseAddr(c.ip)
		err := p.CheckIP(ip)
		if c.blocked && err == nil {
			t.Errorf("%s (%s): expected block, got allow", c.ip, c.why)
		}
		if !c.blocked && err != nil {
			t.Errorf("%s (%s): expected allow, got %v", c.ip, c.why, err)
		}
	}
}

func TestCheckIPAllowlistOverride(t *testing.T) {
	// self-hoster CIDR allowlist mode (§8): explicitly allowed ranges win
	p := Policy{AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}
	if err := p.CheckIP(netip.MustParseAddr("10.0.0.5")); err != nil {
		t.Errorf("allowlisted 10.0.0.5 rejected: %v", err)
	}
	if err := p.CheckIP(netip.MustParseAddr("10.0.1.5")); err == nil {
		t.Errorf("10.0.1.5 outside allowlist was allowed")
	}
}

func TestValidateURL(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		allowHTTP bool
		ok        bool
	}{
		{"https ok", "https://example.com/hook", false, true},
		{"http rejected in hosted mode", "http://example.com/hook", false, false},
		{"http allowed with flag", "http://example.com/hook", true, true},
		{"non-http scheme", "ftp://example.com/x", true, false},
		{"gopher scheme", "gopher://example.com/x", true, false},
		{"empty host", "https:///hook", false, false},
		{"userinfo smuggling", "https://admin:pw@example.com/hook", false, false},
		{"ip literal loopback", "https://127.0.0.1/hook", false, false},
		{"ip literal metadata", "https://169.254.169.254/latest/meta-data/", false, false},
		{"ip literal public", "https://8.8.8.8/hook", false, true},
		{"v6 literal loopback", "https://[::1]/hook", false, false},
	}
	for _, c := range cases {
		p := Policy{AllowHTTP: c.allowHTTP}
		err := p.ValidateURL(c.url)
		if c.ok && err != nil {
			t.Errorf("%s: ValidateURL(%q) = %v, want nil", c.name, c.url, err)
		}
		if !c.ok && err == nil {
			t.Errorf("%s: ValidateURL(%q) = nil, want error", c.name, c.url)
		}
	}
}

func TestPolicyErrorsWrapDomainSentinel(t *testing.T) {
	p := Policy{}
	err := p.CheckIP(netip.MustParseAddr("127.0.0.1"))
	if err == nil {
		t.Fatal("expected error")
	}
	// must classify as permanent/policy via domain.ErrPolicyViolation (§7)
	if got, _ := classifyHelper(err); got != "permanent" {
		t.Fatalf("policy error not classified permanent: %v", err)
	}
}

func classifyHelper(err error) (string, string) {
	o, ec := domainClassify(err)
	return string(o), ec
}
