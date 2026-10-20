package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// The server fetches addresses that people (an admin adding a connector) and models (a web_fetch call a
// web page talked them into) choose. Those must not reach the server's own network: cloud metadata
// services, databases, the app itself. The check happens when the connection is made, on the address
// actually dialled, so DNS tricks and redirects cannot get around it.

type guard struct {
	allowPrivate bool // the operator trusts connectors on their own network (ALLOW_PRIVATE_CONNECTORS)
	anyPort      bool // not just 80 and 443
}

var errBlockedAddress = errors.New("that address is not allowed: it points at a private or internal network")

var extraBlocked = func() []netip.Prefix {
	var out []netip.Prefix
	for _, p := range []string{
		"100.64.0.0/10",   // carrier-grade NAT
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation
		"203.0.113.0/24",  // documentation
		"240.0.0.0/4",     // reserved
		"64:ff9b::/96",    // NAT64
		"2001:db8::/32",   // documentation
	} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

func blockedIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	a = a.Unmap()
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range extraBlocked {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func (g guard) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("could not look up %s", host)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("could not look up %s", host)
	}
	if !g.allowPrivate {
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				return nil, errBlockedAddress
			}
		}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

// client is an HTTP client that only connects where the guard allows. It ignores proxy settings and follows
// at most five redirects, each checked again at connection time.
func (g guard) client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           g.dial,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("redirected to an unsupported address")
			}
			return nil
		},
	}
}

// parseWebURL checks an address before anything connects: http or https, a host, no embedded
// credentials, and (unless anyPort) the standard ports.
func parseWebURL(raw string, anyPort bool) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2000 {
		return nil, errors.New("give a full address starting with https://")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("that is not a valid address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("only http and https addresses are supported")
	}
	if u.User != nil {
		return nil, errors.New("addresses with a username or password are not allowed")
	}
	if p := u.Port(); p != "" && !anyPort && p != "80" && p != "443" {
		return nil, errors.New("only the standard web ports are allowed")
	}
	return u, nil
}
