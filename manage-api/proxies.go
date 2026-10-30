package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// oc-custom: the reverse proxies (SWAG) whose X-Real-IP names the client. Anyone else
// who can reach the port (another container on SWAG's network) is named by
// its own address, whatever header it sends, so it can neither dodge the
// limit on failed attempts nor block another client's address.

// proxyResolve is how long a proxy's name is trusted to keep its addresses.
const proxyResolve = 30 * time.Second

type trustedProxies struct {
	prefixes []netip.Prefix
	names    []string // container names, resolved when used (their addresses change)
	lookup   func(ctx context.Context, host string) ([]netip.Addr, error)

	mu       sync.Mutex
	resolved []netip.Addr
	at       time.Time
}

// parseTrustedProxies reads MANAGE_API_TRUSTED_PROXIES: addresses, ranges
// ("10.0.0.0/8") and host names ("swag"), separated by commas.
func parseTrustedProxies(list string) (*trustedProxies, error) {
	t := &trustedProxies{lookup: func(ctx context.Context, host string) ([]netip.Addr, error) {
		return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}}
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		switch {
		case f == "":
		case strings.Contains(f, "/"):
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("%q: %v", f, err)
			}
			t.prefixes = append(t.prefixes, p.Masked())
		default:
			if a, err := netip.ParseAddr(f); err == nil {
				a = a.Unmap()
				t.prefixes = append(t.prefixes, netip.PrefixFrom(a, a.BitLen()))
			} else if validHost(f) {
				t.names = append(t.names, f)
			} else {
				return nil, fmt.Errorf("%q: an address, a range or a host name", f)
			}
		}
	}
	return t, nil
}

func validHost(s string) bool {
	if len(s) > 253 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}

func (t *trustedProxies) trusted(a netip.Addr) bool {
	if t == nil {
		return false
	}
	a = a.Unmap()
	for _, p := range t.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	if len(t.names) == 0 {
		return false
	}
	for _, r := range t.namesResolved() {
		if r == a {
			return true
		}
	}
	return false
}

// namesResolved returns the proxies' names' addresses, looked up at most
// every proxyResolve; a name that doesn't resolve trusts nothing.
func (t *trustedProxies) namesResolved() []netip.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.at.IsZero() || time.Since(t.at) >= proxyResolve {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var all []netip.Addr
		for _, n := range t.names {
			addrs, _ := t.lookup(ctx, n)
			for _, a := range addrs {
				all = append(all, a.Unmap())
			}
		}
		t.resolved, t.at = all, time.Now()
	}
	return t.resolved
}

// client names who asked, for the limit on failed attempts and the log: the
// connection's address, or a trusted proxy's X-Real-IP.
func (a *api) client(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !a.proxies.trusted(peer) {
		return host
	}
	if ip, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return ip.Unmap().String()
	}
	return host
}
