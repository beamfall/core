package updatecheck

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
)

// vetURL enforces the ADR-0012 §9 pre-dial rules: https only, no literal IP
// hosts. The test-only insecure-loopback gate admits loopback literals (any
// scheme) so httptest servers are reachable.
func (s *Service) vetURL(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("updatecheck: refused URL without a host")
	}
	ip := net.ParseIP(host)
	if s.insecureLoopback && ip != nil && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("updatecheck: refused scheme %q — the version check requires https", u.Scheme)
	}
	if ip != nil {
		return fmt.Errorf("updatecheck: refused literal IP host %q — the version check requires hostnames", host)
	}
	return nil
}

// controlDial is the net.Dialer Control hook: it sees the concrete IP the
// socket is about to connect to (after DNS resolution), so a hostname that
// resolves — or later re-resolves — to a private address is refused here
// regardless of what vetURL saw. This is the DNS-rebinding defence.
func (s *Service) controlDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("updatecheck: refused dial to unparseable address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("updatecheck: refused dial to non-IP address %q", host)
	}
	if s.insecureLoopback && ip.IsLoopback() {
		return nil // TESTS ONLY (WithInsecureLoopback)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("updatecheck: refused dial to non-public address %q", ip)
	}
	return nil
}

// isPublicIP rejects loopback, RFC 1918/ULA private, link-local (which covers
// the 169.254.169.254 cloud metadata endpoint), multicast, and unspecified
// addresses, for both IPv4 and IPv6 (including v4-mapped v6).
func isPublicIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	return !(ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified())
}

// checkRedirect pins redirects to the original host (ADR-0012 §9: no
// cross-host redirect following) and re-vets every hop.
func (s *Service) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return fmt.Errorf("updatecheck: refused after %d redirects", maxRedirects)
	}
	if req.URL.Hostname() != via[0].URL.Hostname() {
		return fmt.Errorf("updatecheck: refused cross-host redirect from %q to %q", via[0].URL.Hostname(), req.URL.Hostname())
	}
	return s.vetURL(req.URL)
}
