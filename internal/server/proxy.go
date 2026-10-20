package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
)

// KnownProxies is the immutable, server-wide trusted reverse-proxy allowlist.
// A nil value means forwarded headers are not trusted.
type KnownProxies struct {
	prefixes []netip.Prefix
}

// ParseKnownProxies parses a comma-separated CIDR allowlist. By default only
// private, loopback, and link-local networks are accepted; public or unusually
// broad networks require the deliberately separate escape hatch.
func ParseKnownProxies(value string, allowBroad bool) (*KnownProxies, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	seen := make(map[netip.Prefix]struct{}, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("parse CIDR %q: %w", strings.TrimSpace(part), err)
		}
		prefix = prefix.Masked()
		if !allowBroad && !safeProxyPrefix(prefix) {
			return nil, fmt.Errorf("CIDR %s is public or over-broad; set BEAMFALL_ALLOW_BROAD_KNOWN_PROXIES=true only after reviewing the trust boundary", prefix)
		}
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return prefixes[i].String() < prefixes[j].String() })
	return &KnownProxies{prefixes: prefixes}, nil
}

func safeProxyPrefix(prefix netip.Prefix) bool {
	// The minimum masks preserve the complete standard private ranges while
	// refusing typo-prone supernets such as 10.0.0.0/7 and fc00::/6.
	minimumBits := 8
	if prefix.Addr().Is6() {
		minimumBits = 7
	}
	if prefix.Bits() < minimumBits {
		return false
	}
	first := prefix.Addr()
	last := prefixLastAddr(prefix)
	return safeProxyAddr(first) && safeProxyAddr(last)
}

func safeProxyAddr(addr netip.Addr) bool {
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast()
}

func prefixLastAddr(prefix netip.Prefix) netip.Addr {
	addr := prefix.Masked().Addr()
	bytes := addr.AsSlice()
	for bit := prefix.Bits(); bit < len(bytes)*8; bit++ {
		bytes[bit/8] |= 1 << (7 - uint(bit%8))
	}
	last, _ := netip.AddrFromSlice(bytes)
	return last
}

func (p *KnownProxies) contains(addr netip.Addr) bool {
	for _, prefix := range p.prefixes {
		if prefix.Contains(addr.Unmap()) || prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (p *KnownProxies) String() string {
	values := make([]string, len(p.prefixes))
	for i, prefix := range p.prefixes {
		values[i] = prefix.String()
	}
	return strings.Join(values, ",")
}

// ProxyResolution is the per-request audit and diagnostics record. OriginalAddr
// is always the real TCP peer; EffectiveAddr is the downstream RemoteAddr.
type ProxyResolution struct {
	OriginalAddr  string
	EffectiveAddr string
	Scheme        string
	Trusted       bool
	// ViaKnownProxy records that the TCP peer is in the operator's declared
	// reverse-proxy allowlist, so the request was relayed rather than opened by
	// the client it speaks for. It stays true when the forwarded address could
	// not be resolved — precisely the case where EffectiveAddr is still the
	// proxy's own address and therefore carries none of the client's standing.
	ViaKnownProxy bool
	Reason        string
}

// forwardedHeaders are the hop headers a reverse proxy adds to a request it
// relays. Their presence is evidence that the request did not originate at the
// TCP peer address, whether or not the proxy is declared in the known-proxies
// allowlist and whether or not the values parse. Only a proxy in front of this
// process can add them to relayed traffic; a client that sets one itself can
// only ever narrow its own access, never widen it.
var forwardedHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Forwarded-Proto",
	"X-Real-Ip",
	"Via",
}

// requestWasForwarded reports whether the request carries reverse-proxy
// forwarding evidence. An empty header value is not evidence: net/http records
// a header explicitly set to "" without carrying a hop.
func requestWasForwarded(r *http.Request) bool {
	for _, name := range forwardedHeaders {
		for _, value := range r.Header.Values(name) {
			if strings.TrimSpace(value) != "" {
				return true
			}
		}
	}
	return false
}

// requestRelayedByProxy reports whether the request reached this process
// through a reverse proxy instead of directly from its client. Both signals are
// resolved host-side: the declared known-proxies allowlist, and the forwarding
// headers a relaying proxy adds. Callers that need "this client is on this
// host" must consult it, because a same-host proxy presents a loopback peer
// address for traffic it relays from anywhere.
func requestRelayedByProxy(r *http.Request) bool {
	if resolution, ok := ProxyResolutionFromRequest(r); ok && resolution.ViaKnownProxy {
		return true
	}
	return requestWasForwarded(r)
}

// requestCarriesForwardedHeader reports whether the request carries a hop
// header at all, whatever its value. It answers a different question from
// requestWasForwarded, which asks whether a header carries a hop that can be
// read as evidence and therefore treats a value trimming to empty as none. A
// declared proxy relaying `X-Forwarded-For:` with an empty field-value resolves
// no client address while producing no such evidence, so an evidence test would
// leave the proxy's own address standing in for the client's. Presence is the
// signal a proximity carve-out must gate on.
func requestCarriesForwardedHeader(r *http.Request) bool {
	for _, name := range forwardedHeaders {
		if len(r.Header.Values(name)) > 0 {
			return true
		}
	}
	return false
}

// requestForwardedUnresolved reports that a declared reverse proxy relayed the
// request but its forwarded address could not be reduced to exactly one value.
// The address left on the request is then the proxy's own, so a proximity check
// that reads it would inherit the proxy's standing rather than the client's —
// the most privileged reading of an ambiguous input. Proximity checks take the
// least privileged reading instead.
//
// The hop-header conjunct is the deliberate carve-out for availability: a
// client that really is on this host, whose address happens to fall inside a
// declared CIDR, sends no hop header, and must keep its standing on the
// ADR-0035 conductor and ADR-0046 pinning paths. It gates on presence rather
// than on evidence so that a proxy relaying a blank hop header — which resolves
// no client and carries no evidence — cannot land in that carve-out.
func requestForwardedUnresolved(r *http.Request) bool {
	resolution, ok := ProxyResolutionFromRequest(r)
	return ok && resolution.ViaKnownProxy && !resolution.Trusted && requestCarriesForwardedHeader(r)
}

type proxyResolutionKey struct{}

// ProxyResolutionFromRequest exposes proxy trust decisions to audit/status
// handlers without creating a second client-identity channel.
func ProxyResolutionFromRequest(r *http.Request) (ProxyResolution, bool) {
	resolution, ok := r.Context().Value(proxyResolutionKey{}).(ProxyResolution)
	return resolution, ok
}

func (s *Server) resolveProxyRequest(r *http.Request) *http.Request {
	if s.knownProxies == nil || requestFromRelay(r) {
		return r
	}
	original := r.RemoteAddr
	resolution := ProxyResolution{OriginalAddr: original, EffectiveAddr: original, Reason: "tcp peer not in known-proxies"}
	peer, ok := remoteIP(original)
	if !ok || !s.knownProxies.contains(peer) {
		return withProxyResolution(r, resolution)
	}
	resolution.ViaKnownProxy = true
	resolution.Reason = "trusted peer; forwarded address absent or invalid"
	forwarded, ok := singleForwardedAddress(r.Header.Values("X-Forwarded-For"))
	if !ok {
		return withProxyResolution(r, resolution)
	}
	port := ""
	if _, p, err := net.SplitHostPort(original); err == nil {
		port = p
	}
	effective := forwarded.String()
	if port != "" {
		effective = net.JoinHostPort(effective, port)
	}
	r2 := r.Clone(r.Context())
	r2.RemoteAddr = effective
	resolution.EffectiveAddr = effective
	resolution.Trusted = true
	resolution.Reason = "trusted single-hop forwarded address"
	if scheme, valid := forwardedScheme(r.Header.Values("X-Forwarded-Proto")); valid {
		resolution.Scheme = scheme
	}
	return withProxyResolution(r2, resolution)
}

func remoteIP(remoteAddr string) (netip.Addr, bool) {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	addr, err := netip.ParseAddr(host)
	return addr.Unmap(), err == nil
}

func singleForwardedAddress(values []string) (netip.Addr, bool) {
	if len(values) != 1 {
		return netip.Addr{}, false
	}
	parts := strings.Split(values[0], ",")
	// A valid multi-hop chain is deliberately unsupported. Invalid prepended
	// text is discarded while parsing from the trusted peer's right-hand side.
	valid := make([]netip.Addr, 0, len(parts))
	for i := len(parts) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err == nil {
			valid = append(valid, addr.Unmap())
		}
	}
	if len(valid) != 1 {
		return netip.Addr{}, false
	}
	return valid[0], true
}

func forwardedScheme(values []string) (string, bool) {
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return "", false
	}
	scheme := strings.ToLower(strings.TrimSpace(values[0]))
	return scheme, scheme == "http" || scheme == "https"
}

func withProxyResolution(r *http.Request, resolution ProxyResolution) *http.Request {
	slog.Debug("reverse proxy resolution", "original_addr", resolution.OriginalAddr, "effective_addr", resolution.EffectiveAddr, "trusted", resolution.Trusted, "via_known_proxy", resolution.ViaKnownProxy, "scheme", resolution.Scheme, "reason", resolution.Reason)
	ctx := context.WithValue(r.Context(), proxyResolutionKey{}, resolution)
	return r.WithContext(ctx)
}
