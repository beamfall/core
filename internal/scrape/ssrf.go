package scrape

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
)

// vetURL enforces the ADR-0012 §9 pre-dial rules: https only, no literal IP
// hosts. The test-only insecure-loopback gate admits loopback literals (any
// scheme) so httptest servers are reachable.
func (e *Engine) vetURL(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return refusedf("scrape: refused URL without a host")
	}
	ip := net.ParseIP(host)
	if e.insecureLoopback && ip != nil && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if u.Scheme != "https" {
		return refusedf("scrape: refused scheme %q — scrapers require https", u.Scheme)
	}
	if ip != nil {
		return refusedf("scrape: refused literal IP host %q — scrapers require hostnames", host)
	}
	return nil
}

// controlDial is the net.Dialer Control hook: it sees the concrete IP the
// socket is about to connect to (after DNS resolution), so a hostname that
// resolves — or later re-resolves — to a private address is refused here
// regardless of what vetURL saw. This is the DNS-rebinding defence.
func (e *Engine) controlDial(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return refusedf("scrape: refused dial to unparseable address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return refusedf("scrape: refused dial to non-IP address %q", host)
	}
	if e.insecureLoopback && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if !IsPublicRoutableIP(ip) {
		return refusedf("scrape: refused dial to non-public address %q", ip)
	}
	return nil
}

// isPublicIP rejects loopback, RFC 1918/ULA private, link-local (which covers
// the 169.254.169.254 cloud metadata endpoint), multicast, and unspecified
// addresses, for both IPv4 and IPv6 (including v4-mapped v6).
// checkRedirect re-vets every hop and pins requests to the original origin.
func (e *Engine) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return refusedf("scrape: refused after %d redirects", maxRedirects)
	}
	if normalizedRedirectOrigin(req.URL) != normalizedRedirectOrigin(via[0].URL) {
		return refusedf("scrape: refused cross-origin redirect")
	}
	return e.vetURL(req.URL)
}

func hasRedirectCredentials(req *http.Request) bool {
	return req.Header.Get("Authorization") != "" || req.URL.User != nil
}

// checkStreamRedirect is checkRedirect for media payloads. Enclosures
// routinely bounce through measurement prefixes (a tracking host, then a CDN),
// so an anonymous request may follow a cross-origin redirect; every hop is
// still re-vetted here and the dialer re-checks the resolved address. A
// credentialed request (Authorization header or URL userinfo) stays pinned to
// its origin, so a feed credential never reaches another origin (CORE-0745).
func (e *Engine) checkStreamRedirect(req *http.Request, via []*http.Request) error {
	first := via[0]
	if hasRedirectCredentials(first) {
		return e.checkRedirect(req, via)
	}
	if len(via) >= maxStreamRedirects {
		return refusedf("scrape: refused after %d redirects", maxStreamRedirects)
	}
	return e.vetURL(req.URL)
}

// SameOrigin reports whether two absolute URLs share a normalized origin
// (scheme, case-insensitive hostname, effective port). Unparseable or
// host-less URLs are never same-origin, so callers scoping credentials with
// it fail closed.
func SameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil || ua.Hostname() == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Hostname() == "" {
		return false
	}
	return normalizedRedirectOrigin(ua) == normalizedRedirectOrigin(ub)
}

func normalizedRedirectOrigin(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return scheme + "://" + net.JoinHostPort(hostname, port)
}

// refusedError is an SSRF-guard refusal. Its message is the one the guard has
// always returned; the type lets callers classify refusals with IsRefused.
type refusedError struct{ msg string }

func (r *refusedError) Error() string { return r.msg }

func refusedf(format string, args ...any) error {
	return &refusedError{msg: fmt.Sprintf(format, args...)}
}

// IsRefused reports whether err is (or wraps) an SSRF-guard refusal: a
// non-https or host-less URL, a literal IP host, a dial to a non-public
// address or a cross-origin redirect.
func IsRefused(err error) bool {
	var refused *refusedError
	return errors.As(err, &refused)
}

// CheckURL applies the pre-dial SSRF rules to rawURL without fetching it.
// The dial-time address check still runs on every later fetch. A parse
// failure is never wrapped: the error would embed the URL.
func (e *Engine) CheckURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return refusedf("scrape: refused invalid URL")
	}
	return e.vetURL(parsed)
}
