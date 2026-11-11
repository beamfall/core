package webhook

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// addrClass is the webhook delivery classification of one destination address.
// The webhook package may import only store and jsonstate (ARCHITECTURE §4),
// and an operator-approved LAN receiver must reach addresses the scraper's
// public-only predicate refuses, so the classes are defined here.
type addrClass int

const (
	// classOther is every address outside the two classes below: ordinary
	// internet destinations.
	classOther addrClass = iota
	// classLocal is reachable only when the endpoint is approved for the local
	// network: loopback, RFC 1918 / ULA private, shared address space, and the
	// deprecated IPv6 site-local block.
	classLocal
	// classRefused is never a webhook destination: unspecified, "this network",
	// link-local (including the 169.254.169.254 cloud metadata endpoint),
	// multicast, the never-routable special-purpose blocks, and IPv6 forms that
	// embed an IPv4 address.
	classRefused
)

var (
	thisNetworkPrefix    = netip.MustParsePrefix("0.0.0.0/8")
	sharedPrefix         = netip.MustParsePrefix("100.64.0.0/10")
	ipv6SiteLocalPrefix  = netip.MustParsePrefix("fec0::/10")
	ipv4CompatiblePrefix = netip.MustParsePrefix("::/96")
)

// neverRoutablePrefixes mirrors the IANA special-purpose blocks that
// internal/scrape/public_routability.go (specialUsePrefixes) refuses, minus
// the local classes above and the blocks classify already refuses by
// predicate. It includes the IPv6 transition prefixes that carry an IPv4
// address: NAT64 (64:ff9b::/96, 64:ff9b:1::/48), 6to4 (2002::/16) and Teredo
// (inside 2001::/23). Those are refused outright, whatever they embed, as the
// scraper refuses them. TestEgressAgreesWithScraperPredicate pins the match.
var neverRoutablePrefixes = func() []netip.Prefix {
	raw := []string{
		"192.0.0.0/24", "192.0.2.0/24", "192.31.196.0/24", "192.52.193.0/24",
		"192.88.99.0/24", "192.175.48.0/24", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "240.0.0.0/4", // 240.0.0.0/4 holds the 255.255.255.255 limited broadcast
		"64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "100:0:0:1::/64", "2001::/23",
		"2001:db8::/32", "2002::/16", "2620:4f:8000::/48", "3fff::/20", "5f00::/16",
	}
	prefixes := make([]netip.Prefix, len(raw))
	for i, value := range raw {
		prefixes[i] = netip.MustParsePrefix(value)
	}
	return prefixes
}()

// neverRoutable reports whether addr (already unmapped) is in a special-purpose
// block or is an IPv4-compatible IPv6 form (::/96 other than :: and ::1, which
// the scraper's registry snapshot does not list but which embeds an IPv4).
func neverRoutable(addr netip.Addr) bool {
	if ipv4CompatiblePrefix.Contains(addr) && !addr.IsLoopback() {
		return true
	}
	for _, prefix := range neverRoutablePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// classify returns the delivery class of addr after canonical unmapping, so
// an IPv4-mapped IPv6 form is classified as the IPv4 address it carries.
func classify(addr netip.Addr) addrClass {
	addr = addr.WithZone("").Unmap()
	switch {
	case !addr.IsValid(), addr.IsUnspecified(), thisNetworkPrefix.Contains(addr),
		addr.IsLinkLocalUnicast(), addr.IsMulticast(), neverRoutable(addr):
		return classRefused
	case addr.IsLoopback(), addr.IsPrivate(), sharedPrefix.Contains(addr), ipv6SiteLocalPrefix.Contains(addr):
		return classLocal
	}
	return classOther
}

var errUnsafeEndpointURL = errors.New("webhook endpoint URL is malformed or unsafe")

// endpointAllowsLocal validates an endpoint URL before any delivery and reports
// whether its host approves local-class destinations. A URL must be an
// absolute http(s) URL with a host and no user info (credentials belong in the
// secret store, invariant #4). An IP-literal host in the refused class is
// rejected; one in the local class is the operator naming a LAN receiver, which
// approves local destinations. A hostname approves them only through the
// endpoint's AllowPrivateNetwork opt-in.
func endpointAllowsLocal(ep Endpoint) (bool, error) {
	u, err := url.Parse(ep.URL)
	if err != nil || u.Opaque != "" || u.User != nil {
		return false, errUnsafeEndpointURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false, errUnsafeEndpointURL
	}
	host := u.Hostname()
	if host == "" {
		return false, errUnsafeEndpointURL
	}
	if port := u.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			return false, errUnsafeEndpointURL // a port no dial can reach
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" {
			return false, errUnsafeEndpointURL // zoned literals are link-scoped
		}
		switch classify(addr) {
		case classRefused:
			return false, errUnsafeEndpointURL
		case classLocal:
			return true, nil
		}
	}
	return ep.AllowPrivateNetwork, nil
}

// lanHostnameSuffixes are DNS suffixes conventionally served only on a home
// or office network (mDNS, RFC 8375, and common router defaults).
var lanHostnameSuffixes = []string{".local", ".lan", ".home", ".internal", ".localdomain", ".home.arpa"}

// looksLikeLANHostname reports whether an endpoint URL names a hostname (not an
// IP literal) that conventionally resolves to the local network: localhost, a
// single-label name, or a LAN suffix. It does no DNS lookup, so it is only a
// hint for the startup warning in New, never a security decision.
func looksLikeLANHostname(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range lanHostnameSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

var errDialRefused = errors.New("webhook refused dial to a disallowed address class")

// checkDialAddress is the net.Dialer Control hook body: it sees the concrete
// IP the socket is about to connect to after DNS resolution, so a hostname
// that resolves (or re-resolves) into a refused or unapproved local class is
// stopped before any byte of the signed delivery leaves the process.
func checkDialAddress(address string, allowLocal bool) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errDialRefused
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return errDialRefused
	}
	switch classify(addr) {
	case classRefused:
		return errDialRefused
	case classLocal:
		if !allowLocal {
			return errDialRefused
		}
	}
	return nil
}

// refuseRedirect stops the client at the first redirect and hands the 3xx back
// to post, where it is a non-2xx failed attempt. Following a 307/308 would
// replay the signed POST body to whatever origin the endpoint names.
func refuseRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// newDeliveryClient builds the bounded default delivery client for one
// endpoint: a fixed overall timeout, no redirects, no environment proxy (a
// proxy dial would hide the destination from the Control hook), and a dial
// Control hook enforcing the endpoint's address classes.
func newDeliveryClient(allowLocal bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			return checkDialAddress(address, allowLocal)
		},
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{
		Timeout:       defaultHTTPTimeout,
		Transport:     transport,
		CheckRedirect: refuseRedirect,
	}
}
