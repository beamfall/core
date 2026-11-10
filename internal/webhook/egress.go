package webhook

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
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
	// multicast, and IPv4 limited broadcast.
	classRefused
)

var (
	thisNetworkPrefix   = netip.MustParsePrefix("0.0.0.0/8")
	sharedPrefix        = netip.MustParsePrefix("100.64.0.0/10")
	ipv6SiteLocalPrefix = netip.MustParsePrefix("fec0::/10")
	ipv4Broadcast       = netip.AddrFrom4([4]byte{255, 255, 255, 255})
)

// classify returns the delivery class of addr after canonical unmapping, so
// an IPv4-mapped IPv6 form is classified as the IPv4 address it carries.
func classify(addr netip.Addr) addrClass {
	addr = addr.WithZone("").Unmap()
	switch {
	case !addr.IsValid(), addr.IsUnspecified(), thisNetworkPrefix.Contains(addr),
		addr.IsLinkLocalUnicast(), addr.IsMulticast(), addr == ipv4Broadcast:
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
