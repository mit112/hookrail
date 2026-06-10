package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/mit112/hookrail/internal/domain"
)

// Dialer resolves a hostname once, validates EVERY returned IP against the
// policy, then dials the first validated IP directly. The connection target
// is the pinned IP — the OS never re-resolves, so a DNS answer that changes
// between validation and connect (rebinding) cannot redirect the dial.
type Dialer struct {
	Policy Policy
	// Lookup and DialFn are injectable for tests; nil → real resolver/dialer.
	Lookup func(ctx context.Context, host string) ([]netip.Addr, error)
	DialFn func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (d *Dialer) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if d.Lookup != nil {
		return d.Lookup(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func (d *Dialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.DialFn != nil {
		return d.DialFn(ctx, network, addr)
	}
	nd := &net.Dialer{Timeout: 3 * time.Second} // connect budget (§8)
	return nd.DialContext(ctx, network, addr)
}

func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip, perr := netip.ParseAddr(host); perr == nil {
		if err := d.Policy.CheckIP(ip); err != nil {
			return nil, err
		}
		return d.dial(ctx, network, addr)
	}
	addrs, err := d.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: no addresses resolved for %s", domain.ErrPolicyViolation, host)
	}
	for _, ip := range addrs {
		if err := d.Policy.CheckIP(ip); err != nil {
			return nil, err // ANY blocked answer poisons the host
		}
	}
	pinned := addrs[0].Unmap()
	return d.dial(ctx, network, net.JoinHostPort(pinned.String(), port))
}
