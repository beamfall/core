package remoteaccess

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Endpoint ordering + overlay detection.
//
// ADR-0012 §1 makes dual-URL "the normal connection model": a client stores the
// ordered set of URLs it can reach this server by and selects automatically.
// This file builds that ordered set server-side so the contract is one tested
// resolver, not logic re-derived in each client.
//
// The canonical path order is fixed and deterministic (ADR-0025 §3, restated as
// the tested client connection contract in ADR-0018 §3):
//
//	LAN → direct HTTPS → overlay network → self-hosted relay → hosted relay
//
// A LAN-only server returns only its LAN URL; each later path appears only when
// configured (direct/relay/hosted) or detected (overlay). No path is ever faked.
// Relay kinds are last by construction — the client selection rule is "try
// candidates in the emitted order, relay last".

// EndpointKind names a remote-access path by SHAPE, never by domain (invariant
// #1). The five kinds are a CLOSED, STABLE vocabulary mapping 1:1 to
// the canonical order positions; clients ship it compiled in and rely on the
// order. Adding a kind is a contract change (extend EndpointKinds + kindOrder +
// the closed-vocab test together).
type EndpointKind string

const (
	// EndpointLAN is a private/link-local address the server listens on,
	// reachable only from the same network. Always first.
	EndpointLAN EndpointKind = "lan"
	// EndpointDirect is the operator-configured direct-HTTPS/ACME domain
	//. Second.
	EndpointDirect EndpointKind = "direct"
	// EndpointOverlay is an address on a detected overlay interface
	// (Tailscale/WireGuard, ADR-0012 §1). Third.
	EndpointOverlay EndpointKind = "overlay"
	// EndpointSelfHostedRelay is the operator-run self-hosted relay's public
	// host. Fourth — first of the relay (last-resort) kinds.
	EndpointSelfHostedRelay EndpointKind = "self-hosted-relay"
	// EndpointHosted is the future Beamfall Hosted Relay's public host
	// (ADR-0018, demoted to future by ADR-0025 §1). Last — the path of last
	// resort. The server emits only the generic connection candidate (a host
	// string); the hosted-relay *entitlement* lives in the proprietary mint,
	// never here (invariant #7). Omitted unless a hosted host is configured.
	EndpointHosted EndpointKind = "hosted"
)

// EndpointKinds is the closed, ordered vocabulary of path kinds. It is
// the single source of truth for both the membership check (a kind not here is
// not part of the contract) and the canonical order (index = priority). Relay
// kinds are last.
var EndpointKinds = []EndpointKind{
	EndpointLAN,
	EndpointDirect,
	EndpointOverlay,
	EndpointSelfHostedRelay,
	EndpointHosted,
}

// kindOrder is the canonical sort key for each kind, derived from
// EndpointKinds so the two cannot drift. Lower sorts earlier; the resolver
// relies on this to keep ordering deterministic regardless of the order paths
// are appended.
var kindOrder = func() map[EndpointKind]int {
	m := make(map[EndpointKind]int, len(EndpointKinds))
	for i, k := range EndpointKinds {
		m[k] = i
	}
	return m
}()

// Endpoint is one reachable URL plus the path kind it represents. URL is a
// fully-formed base URL (scheme + host[:port]) a client connects to; it carries
// no secret.
type Endpoint struct {
	Kind EndpointKind `json:"kind"`
	URL  string       `json:"url"`
}

// EndpointInputs are the configured/runtime facts the resolver turns into the
// ordered URL set. Each is independent: a LAN-only server supplies only LANPort,
// leaving Direct/Relay empty, and the resolver detects no overlay.
type EndpointInputs struct {
	// LANScheme is the scheme the LAN listener serves with ("http" for the
	// default plain-HTTP LAN server, "https" if fronted). LAN cleartext is
	// permitted to private destinations only; remote/overlay/relay
	// paths always use https.
	LANScheme string
	// LANPort is the TCP port the server listens on. Required to build LAN and
	// overlay URLs; when 0 those paths are omitted.
	LANPort int
	// DirectDomain is the configured direct-HTTPS public FQDN (empty = path off).
	DirectDomain string
	// DirectTLSActive reports whether the server is currently serving a TLS
	// listener for DirectDomain. A configured domain alone is not advertised.
	DirectTLSActive bool
	// OverlayTLSActive reports whether the server is actually terminating client
	// TLS on the LAN port (self-signed baseline identity or ACME; NET-02). The
	// overlay candidate is emitted as https, so it is advertised ONLY when a TLS
	// identity truly backs the port — otherwise the "blessed remote path" would
	// be a dead endpoint that never handshakes (the ADR-0012 §1 overlay bug this
	// resolver used to reproduce). Every server now holds a self-signed identity,
	// so this is true in practice; it is a gate, never faked.
	OverlayTLSActive bool
	// SelfHostedRelayPublicHost is the SNI host the operator's self-hosted relay
	// routes inbound by (empty = path off; ADR-0025 §2).
	SelfHostedRelayPublicHost string
	// HostedPublicHost is the SNI host the future Beamfall Hosted Relay routes
	// inbound by (empty = path off). Hosted Relay is demoted to future
	//, so this is empty at launch; it is a generic host string,
	// never any entitlement state (invariant #7).
	HostedPublicHost string
	// interfaces is the interface list source (name + addrs), injectable for
	// tests. Nil uses the real host interfaces. LAN and overlay classification
	// share this snapshot so an overlay address cannot also become a LAN URL.
	interfaces func() ([]overlayIface, error)
}

// overlayIface is a minimal interface view (name + its addresses) for overlay
// detection, decoupled from net.Interface so tests can inject fixtures.
type overlayIface struct {
	name  string
	addrs []net.Addr
}

// ResolveEndpoints returns the ordered remote-access URL set in the canonical
// path order (LAN → direct → overlay → self-hosted-relay → hosted; relay kinds
// last per the SEC-10 selection rule). The result is deterministic: it
// is sorted by kind, and within a kind URLs are sorted lexically, so repeated
// calls on unchanged inputs return byte-identical lists. A path with no input
// (or, for overlay, no detected interface) is omitted, never faked.
func ResolveEndpoints(in EndpointInputs) []Endpoint {
	var out []Endpoint
	var ifaces []overlayIface
	if in.LANPort > 0 {
		src := in.interfaces
		if src == nil {
			src = realInterfaces
		}
		if found, err := src(); err == nil {
			ifaces = found
		}
	}
	overlayAddrs := overlayIPs(ifaces)

	// 1. LAN: private/link-local addresses outside the overlay paths.
	if in.LANPort > 0 {
		scheme := in.LANScheme
		if scheme == "" {
			scheme = "http"
		}
		for _, ip := range lanIPs(ifaces, overlayAddrs) {
			out = append(out, Endpoint{
				Kind: EndpointLAN,
				URL:  buildURL(scheme, ip.String(), in.LANPort),
			})
		}
	}

	// 2. Direct HTTPS: the operator-configured public domain (always https).
	if d := strings.TrimSpace(in.DirectDomain); d != "" && in.DirectTLSActive {
		out = append(out, Endpoint{Kind: EndpointDirect, URL: buildURL("https", d, 0)})
	}

	// 3. Overlay: addresses on a detected Tailscale/WireGuard interface. Remote
	// path → https, so it is advertised only when a TLS identity actually
	// terminates on the LAN port (OverlayTLSActive; NET-02). Omitted when no
	// overlay interface is present (the LAN-only / no-overlay case) OR when no TLS
	// identity backs the port — never advertising a scheme the listener can't
	// serve.
	if in.LANPort > 0 && in.OverlayTLSActive {
		for _, ip := range overlayAddrs {
			out = append(out, Endpoint{
				Kind: EndpointOverlay,
				URL:  buildURL("https", ip.String(), in.LANPort),
			})
		}
	}

	// 4. Self-hosted relay: the operator's relay public host (always https).
	if h := strings.TrimSpace(in.SelfHostedRelayPublicHost); h != "" {
		out = append(out, Endpoint{Kind: EndpointSelfHostedRelay, URL: buildURL("https", h, 0)})
	}

	// 5. Hosted relay: the Beamfall Hosted Relay public host (always https),
	// last. Demoted to future — omitted unless configured; the
	// server emits only the generic candidate, never any entitlement.
	if h := strings.TrimSpace(in.HostedPublicHost); h != "" {
		out = append(out, Endpoint{Kind: EndpointHosted, URL: buildURL("https", h, 0)})
	}

	sortEndpoints(out)
	return out
}

// sortEndpoints orders endpoints by canonical kind, then by URL within a kind,
// so the contract is deterministic regardless of append order or the order the
// host reports its interfaces.
func sortEndpoints(eps []Endpoint) {
	sort.SliceStable(eps, func(i, j int) bool {
		ki, kj := kindOrder[eps[i].Kind], kindOrder[eps[j].Kind]
		if ki != kj {
			return ki < kj
		}
		return eps[i].URL < eps[j].URL
	})
}

// buildURL forms a base URL from scheme/host/port. port == 0 omits the port
// (the host already carries the canonical 443/standard port for a domain). IPv6
// hosts are bracketed.
func buildURL(scheme, host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port == 0 {
		return scheme + "://" + host
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// lanIPs returns private/link-local IPs on ordinary interfaces only.
func lanIPs(ifaces []overlayIface, overlayAddrs []net.IP) []net.IP {
	overlaySet := make(map[string]bool, len(overlayAddrs))
	for _, ip := range overlayAddrs {
		overlaySet[ip.String()] = true
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if isOverlayName(iface.name) {
			continue
		}
		for _, a := range iface.addrs {
			ip := addrIP(a)
			if ip == nil || ip.IsLoopback() || overlaySet[ip.String()] {
				continue
			}
			if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

// overlayIPs returns IPs that belong to a detected overlay network: either an
// address on an interface named like an overlay device (tailscale0, wg*) or any
// address inside the Tailscale CGNAT range (100.64.0.0/10). Name and CIDR are
// independent signals; either qualifies, and an IP is returned once.
func overlayIPs(ifaces []overlayIface) []net.IP {
	seen := map[string]bool{}
	var ips []net.IP
	for _, iface := range ifaces {
		nameMatch := isOverlayName(iface.name)
		for _, a := range iface.addrs {
			ip := addrIP(a)
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if !nameMatch && !isOverlayCIDR(ip) {
				continue
			}
			if seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			ips = append(ips, ip)
		}
	}
	return ips
}

// realInterfaces adapts net.Interfaces to the overlayIface view.
func realInterfaces() ([]overlayIface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]overlayIface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		out = append(out, overlayIface{name: iface.Name, addrs: addrs})
	}
	return out, nil
}

// addrIP extracts the IP from a net.Addr (*net.IPNet or *net.IPAddr).
func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPNet:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		return nil
	}
}

// isOverlayName reports whether an interface name looks like a Tailscale or
// WireGuard device: "tailscale" prefix (tailscale0, utun on macOS is NOT matched
// — Tailscale uses utun there, so CIDR is the reliable signal cross-platform) or
// a "wg" prefix (wg0, wg-home).
func isOverlayName(name string) bool {
	n := strings.ToLower(name)
	return strings.HasPrefix(n, "tailscale") || strings.HasPrefix(n, "wg")
}

// tailscaleCGNAT is Tailscale's address range (100.64.0.0/10, the RFC 6598
// shared CGNAT block). An IP inside it is treated as overlay regardless of the
// interface name, so detection works on macOS where the device is a generic
// utun.
var tailscaleCGNAT = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// isOverlayCIDR reports whether an IP falls in a known overlay range.
func isOverlayCIDR(ip net.IP) bool {
	return tailscaleCGNAT != nil && tailscaleCGNAT.Contains(ip)
}
