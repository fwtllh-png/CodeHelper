package egress

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// ErrDenied is returned (via errors.Is) when RoundTrip targets a host the Gate
// has not granted. The string is stable for CLI / probe callers.
var ErrDenied = errors.New("egress denied")

// Gate is a session-scoped host allowlist. A zero Gate denies everything once
// Enforce is true; with Enforce false (or a nil *Gate on WrapClient) traffic
// passes through unchanged so unit tests that never wired a broker keep working.
type Gate struct {
	mu      sync.RWMutex
	Enforce bool
	allowed map[string]struct{}
}

func key(host, protocol string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		protocol = "https"
	}
	return protocol + "://" + host
}

// Allow grants host+protocol for subsequent RoundTrips.
func (g *Gate) Allow(host, protocol string) {
	if g == nil {
		return
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.allowed == nil {
		g.allowed = make(map[string]struct{})
	}
	g.allowed[key(host, protocol)] = struct{}{}
}

// AllowURL grants the host+scheme of a URL or bare endpoint string.
func (g *Gate) AllowURL(raw string) bool {
	target, ok := policy.ParseNetworkTarget(raw)
	if !ok {
		return false
	}
	g.Allow(target.Host, target.Protocol)
	return true
}

// Allowed reports whether host+protocol is on the allowlist.
func (g *Gate) Allowed(host, protocol string) bool {
	if g == nil {
		return true
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.Enforce {
		return true
	}
	_, ok := g.allowed[key(host, protocol)]
	return ok
}

// Check returns ErrDenied when Enforce is on and the request URL is not granted.
func (g *Gate) Check(req *http.Request) error {
	if g == nil || !g.Enforce {
		return nil
	}
	if req == nil || req.URL == nil {
		return fmt.Errorf("%w: missing request URL", ErrDenied)
	}
	host := req.URL.Hostname()
	if host == "" {
		host = strings.TrimSpace(req.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	protocol := strings.ToLower(req.URL.Scheme)
	if protocol == "" {
		protocol = "https"
	}
	if g.Allowed(host, protocol) {
		return nil
	}
	return fmt.Errorf("%w: host %s protocol %s is not granted", ErrDenied, strings.ToLower(host), protocol)
}

// RoundTripper wraps base and refuses ungranted hosts when Enforce is on.
func (g *Gate) RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{gate: g, base: base}
}

// WrapClient returns a shallow clone of client whose Transport is gated.
// A nil gate returns client unchanged.
func WrapClient(client *http.Client, gate *Gate) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	if gate == nil {
		return client
	}
	clone := *client
	clone.Transport = gate.RoundTripper(client.Transport)
	return &clone
}

type transport struct {
	gate *Gate
	base http.RoundTripper
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.gate.Check(req); err != nil {
		return nil, err
	}
	// Redirects re-enter RoundTrip with a new URL; Check runs again.
	return t.base.RoundTrip(req)
}

// HostOf is a small helper for callers that already have a parsed URL.
func HostOf(u *url.URL) (host, protocol string, ok bool) {
	if u == nil || u.Hostname() == "" {
		return "", "", false
	}
	protocol = strings.ToLower(u.Scheme)
	if protocol == "" {
		protocol = "https"
	}
	return strings.ToLower(u.Hostname()), protocol, true
}
