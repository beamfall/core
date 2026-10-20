package scrape

import (
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
		return fmt.Errorf("scrape: refused URL without a host")
	}
	ip := net.ParseIP(host)
	if e.insecureLoopback && ip != nil && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("scrape: refused scheme %q — scrapers require https", u.Scheme)
	}
	if ip != nil {
		return fmt.Errorf("scrape: refused literal IP host %q — scrapers require hostnames", host)
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
		return fmt.Errorf("scrape: refused dial to unparseable address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("scrape: refused dial to non-IP address %q", host)
	}
	if e.insecureLoopback && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if !IsPublicRoutableIP(ip) {
		return fmt.Errorf("scrape: refused dial to non-public address %q", ip)
	}
	return nil
}

// isPublicIP rejects loopback, RFC 1918/ULA private, link-local (which covers
// the 169.254.169.254 cloud metadata endpoint), multicast, and unspecified
// addresses, for both IPv4 and IPv6 (including v4-mapped v6).
// checkRedirect pins redirects to the original normalized origin (scheme,
// hostname, and effective port) and re-vets every hop. net/http copies
// Authorization headers across same-host redirects, so comparing Hostname alone
// would leak credentials to another service listening on a different port.
func (e *Engine) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("scrape: refused after %d redirects", maxRedirects)
	}
	original := normalizedRedirectOrigin(via[0].URL)
	redirected := normalizedRedirectOrigin(req.URL)
	if original != redirected {
		return fmt.Errorf("scrape: refused cross-origin redirect from %q to %q", original, redirected)
	}
	return e.vetURL(req.URL)
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
