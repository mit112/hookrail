package ssrf

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/mit112/hookrail/internal/domain"
)

// fakeConn satisfies net.Conn minimally for dial-capture tests.
type fakeConn struct{ net.Conn }

func TestDialerPinsResolvedIP(t *testing.T) {
	var dialed string
	d := &Dialer{
		Policy: Policy{},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		DialFn: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialed = addr
			return fakeConn{}, nil
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	// the dial target is the VALIDATED IP, not the hostname — no re-resolution,
	// which is the DNS-rebinding defense (§8)
	if dialed != "93.184.216.34:443" {
		t.Fatalf("dialed %q, want pinned 93.184.216.34:443", dialed)
	}
}

func TestDialerRejectsWhenAnyResolvedIPBlocked(t *testing.T) {
	d := &Dialer{
		Policy: Policy{},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			// rebinding-style answer: one public, one internal
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("10.0.0.5"),
			}, nil
		},
		DialFn: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.Fatal("dial must not happen when any resolved IP is blocked")
			return nil, nil
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "evil.example:443")
	if !errors.Is(err, domain.ErrPolicyViolation) {
		t.Fatalf("want ErrPolicyViolation, got %v", err)
	}
}

func TestDialerChecksIPLiteralsWithoutLookup(t *testing.T) {
	d := &Dialer{
		Policy: Policy{},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			t.Fatal("lookup must not run for IP literals")
			return nil, nil
		},
		DialFn: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.Fatal("dial must not happen for blocked literal")
			return nil, nil
		},
	}
	_, err := d.DialContext(context.Background(), "tcp", "169.254.169.254:80")
	if !errors.Is(err, domain.ErrPolicyViolation) {
		t.Fatalf("want ErrPolicyViolation, got %v", err)
	}
}

func TestDialerEmptyResolution(t *testing.T) {
	d := &Dialer{
		Policy: Policy{},
		Lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return nil, nil
		},
		DialFn: func(ctx context.Context, network, addr string) (net.Conn, error) {
			t.Fatal("dial must not happen with no addresses")
			return nil, nil
		},
	}
	if _, err := d.DialContext(context.Background(), "tcp", "nxdomain.example:443"); err == nil {
		t.Fatal("want error for empty resolution")
	}
}
