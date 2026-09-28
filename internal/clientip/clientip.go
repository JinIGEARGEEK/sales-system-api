// Package clientip resolves the address a request really came from, for
// per-caller rate limiting (routes.go's login limiter), without letting the
// caller choose it.
//
// X-Forwarded-For is client-writable, so it's only read when the socket
// peer is a trusted proxy (config.TrustedProxies), and then right to left,
// skipping trusted hops: the first untrusted entry is the address the
// outermost trusted proxy saw connect. Anything a client prepended sits to
// the left of that and is never reached.
package clientip

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

// Resolver resolves client addresses against a fixed trusted-proxy list.
type Resolver struct {
	nets []*net.IPNet

	// warnOnce limits the "X-Forwarded-For from an untrusted peer" hint to
	// one line per process — on a misconfigured deployment it'd otherwise
	// fire on every request.
	warnOnce sync.Once
}

// New parses entries (IPs or CIDRs, as in config.TrustedProxies). A bare IP
// is treated as a single-host range. An unparsable entry is an error, so a
// typo fails at boot instead of silently trusting less than intended.
func New(entries []string) (*Resolver, error) {
	r := &Resolver{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				return nil, fmt.Errorf("invalid trusted proxy %q", e)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			r.nets = append(r.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", e, err)
		}
		r.nets = append(r.nets, n)
	}
	return r, nil
}

// Trusted reports whether ip is in the trusted-proxy list.
func (r *Resolver) Trusted(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range r.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP returns the caller's address for c: the socket peer unless that
// peer is a trusted proxy, in which case the rightmost untrusted
// X-Forwarded-For entry (see the package doc). Falls back to the socket
// peer when the header is absent or holds nothing usable, and to the
// leftmost valid entry when every hop is trusted (a request that
// originated inside the trusted network itself).
func (r *Resolver) ClientIP(c *fiber.Ctx) string {
	peer := c.Context().RemoteIP()
	xff := c.Get(fiber.HeaderXForwardedFor)
	if !r.Trusted(peer) {
		if xff != "" {
			r.warnOnce.Do(func() {
				log.Printf("clientip: ignoring X-Forwarded-For from untrusted peer %s — if this is your load balancer/proxy, add it to TRUSTED_PROXIES or every caller shares one login rate-limit bucket", peer)
			})
		}
		return peer.String()
	}

	hops := strings.Split(xff, ",")
	var leftmost net.IP
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(hops[i]))
		if ip == nil {
			// A garbage hop can only have come from the client (a proxy
			// writes real addresses), so nothing left of it is trustworthy
			// either — stop here and key on the proxy that handed it over.
			break
		}
		if !r.Trusted(ip) {
			return ip.String()
		}
		leftmost = ip
	}
	if leftmost != nil {
		return leftmost.String()
	}
	return peer.String()
}
