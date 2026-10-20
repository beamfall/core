package remoteaccess

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

// ipnet builds a *net.IPNet net.Addr from a CIDR for interface-fixture use.
func ipnet(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR %q: %v", cidr, err)
	}
	n.IP = ip
	return n
}

// feature:dual-url-endpoints — a LAN-only server (no direct, no relay, no
// overlay interface) returns exactly one endpoint: its LAN URL.
func TestResolveEndpointsLANOnly(t *testing.T) {
	in := EndpointInputs{
		LANScheme: "http",
		LANPort:   8800,
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{{name: "en0", addrs: []net.Addr{ipnet(t, "127.0.0.1/8"), ipnet(t, "192.168.1.20/24")}}}, nil
		},
	}
	got := ResolveEndpoints(in)
	want := []Endpoint{{Kind: EndpointLAN, URL: "http://192.168.1.20:8800"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LAN-only endpoints = %+v, want %+v", got, want)
	}
}

// feature:dual-url-endpoints — with direct + overlay + both relay kinds all
// present, the resolver returns the canonical path order LAN → direct → overlay
// → self-hosted-relay → hosted, deterministically with relay kinds last
// (ADR-0012 §1 / 0018 §3 / 0025 §3, SEC-10).
func TestResolveEndpointsFullOrder(t *testing.T) {
	in := EndpointInputs{
		LANScheme:                 "http",
		LANPort:                   8800,
		DirectDomain:              "home.example.com",
		DirectTLSActive:           true,
		OverlayTLSActive:          true,
		SelfHostedRelayPublicHost: "relay.example.com",
		HostedPublicHost:          "abc123.relay.beamfall.com",
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{
				{name: "en0", addrs: []net.Addr{ipnet(t, "192.168.1.20/24"), ipnet(t, "127.0.0.1/8")}},
				{name: "wg0", addrs: []net.Addr{ipnet(t, "10.7.0.2/32")}},     // WireGuard by name
				{name: "utun3", addrs: []net.Addr{ipnet(t, "100.96.0.5/32")}}, // Tailscale by CIDR
			}, nil
		},
	}
	got := ResolveEndpoints(in)
	want := []Endpoint{
		{Kind: EndpointLAN, URL: "http://192.168.1.20:8800"},
		{Kind: EndpointDirect, URL: "https://home.example.com"},
		{Kind: EndpointOverlay, URL: "https://10.7.0.2:8800"},
		{Kind: EndpointOverlay, URL: "https://100.96.0.5:8800"},
		{Kind: EndpointSelfHostedRelay, URL: "https://relay.example.com"},
		{Kind: EndpointHosted, URL: "https://abc123.relay.beamfall.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("full-order endpoints =\n%+v\nwant\n%+v", got, want)
	}
	// The canonical kind sequence is the load-bearing contract; assert it, and
	// that the two relay kinds are last (SEC-10 selection rule).
	wantKinds := []EndpointKind{EndpointLAN, EndpointDirect, EndpointOverlay, EndpointOverlay, EndpointSelfHostedRelay, EndpointHosted}
	var gotKinds []EndpointKind
	for _, e := range got {
		gotKinds = append(gotKinds, e.Kind)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kind order = %v, want %v", gotKinds, wantKinds)
	}
}

// CORE-PUB-8 — a configured direct domain is not advertised until the app has
// a live TLS listener serving that certificate.
func TestResolveEndpointsOmitsDirectWhenTLSInactive(t *testing.T) {
	in := EndpointInputs{
		DirectDomain: "home.example.com",
	}
	if got := ResolveEndpoints(in); len(got) != 0 {
		t.Fatalf("inactive direct endpoints = %+v, want none", got)
	}

	in.DirectTLSActive = true
	got := ResolveEndpoints(in)
	want := []Endpoint{{Kind: EndpointDirect, URL: "https://home.example.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active direct endpoints = %+v, want %+v", got, want)
	}
}

// NET-02 — the overlay https candidate is advertised ONLY when a TLS identity
// actually terminates on the LAN port. Without OverlayTLSActive the overlay path
// is a dead endpoint (advertised https, served plaintext), so it must be omitted;
// with it, the same detected overlay IP surfaces. This pins the fix for the
// ADR-0012 §1 overlay dead-endpoint.
func TestResolveEndpointsOmitsOverlayWhenTLSInactive(t *testing.T) {
	in := EndpointInputs{
		LANScheme: "http", LANPort: 8800,
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{
				{name: "en0", addrs: []net.Addr{ipnet(t, "192.168.1.20/24")}},
				{name: "wg0", addrs: []net.Addr{ipnet(t, "10.7.0.2/32")}},
			}, nil
		},
	}
	// OverlayTLSActive false: overlay omitted, only LAN survives.
	got := ResolveEndpoints(in)
	want := []Endpoint{{Kind: EndpointLAN, URL: "http://192.168.1.20:8800"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay-off endpoints = %+v, want %+v (overlay must not be a dead https endpoint)", got, want)
	}
	// OverlayTLSActive true: the overlay candidate now handshakes and is emitted.
	in.OverlayTLSActive = true
	got = ResolveEndpoints(in)
	want = []Endpoint{
		{Kind: EndpointLAN, URL: "http://192.168.1.20:8800"},
		{Kind: EndpointOverlay, URL: "https://10.7.0.2:8800"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay-on endpoints = %+v, want %+v", got, want)
	}
}

func TestResolveEndpointsWireGuardAddressNeverBecomesLAN(t *testing.T) {
	in := EndpointInputs{
		LANScheme: "http", LANPort: 8800, OverlayTLSActive: true,
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{
				{name: "en0", addrs: []net.Addr{ipnet(t, "192.168.1.20/24"), ipnet(t, "10.7.0.2/32")}},
				{name: "wg0", addrs: []net.Addr{ipnet(t, "10.7.0.2/32")}},
			}, nil
		},
	}
	want := []Endpoint{
		{Kind: EndpointLAN, URL: "http://192.168.1.20:8800"},
		{Kind: EndpointOverlay, URL: "https://10.7.0.2:8800"},
	}
	if got := ResolveEndpoints(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("WireGuard endpoints = %+v, want %+v", got, want)
	}

	in.OverlayTLSActive = false
	want = want[:1]
	if got := ResolveEndpoints(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("TLS-inactive WireGuard endpoints = %+v, want %+v", got, want)
	}
}

func TestResolveEndpointsInterfaceInspectionFailureOmitsLocalCandidates(t *testing.T) {
	in := EndpointInputs{
		LANScheme: "http", LANPort: 8800,
		DirectDomain: "home.example.com", DirectTLSActive: true,
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{{name: "en0", addrs: []net.Addr{ipnet(t, "10.7.0.2/32")}}}, errors.New("interface inspection failed")
		},
	}
	want := []Endpoint{{Kind: EndpointDirect, URL: "https://home.example.com"}}
	if got := ResolveEndpoints(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("inspection-failed endpoints = %+v, want %+v", got, want)
	}
}

// SEC-10 — the path-kind vocabulary is closed and stable: EndpointKinds is
// exactly the five contract kinds, in canonical order, and kindOrder is derived
// from it so the two cannot drift. Relay kinds are last.
func TestEndpointKindsClosedAndOrdered(t *testing.T) {
	want := []EndpointKind{"lan", "direct", "overlay", "self-hosted-relay", "hosted"}
	if !reflect.DeepEqual(EndpointKinds, want) {
		t.Fatalf("EndpointKinds = %v, want %v (closed SEC-10 vocab)", EndpointKinds, want)
	}
	// The two relay kinds must occupy the final positions (selection rule).
	if EndpointKinds[len(EndpointKinds)-2] != EndpointSelfHostedRelay ||
		EndpointKinds[len(EndpointKinds)-1] != EndpointHosted {
		t.Fatalf("relay kinds are not last: %v", EndpointKinds)
	}
	// kindOrder must agree with EndpointKinds index for index and have no extra
	// keys (closed: nothing outside the vocab carries an order).
	if len(kindOrder) != len(EndpointKinds) {
		t.Fatalf("kindOrder has %d keys, want %d (vocab not closed)", len(kindOrder), len(EndpointKinds))
	}
	for i, k := range EndpointKinds {
		if kindOrder[k] != i {
			t.Fatalf("kindOrder[%q] = %d, want %d", k, kindOrder[k], i)
		}
	}
}

// SEC-10 — self-hosted-relay configured without hosted emits only the
// self-hosted-relay candidate (hosted omitted, demoted to future per
// ADR-0025 §1); a candidate with no input is never faked.
func TestResolveEndpointsHostedOmittedWhenUnconfigured(t *testing.T) {
	in := EndpointInputs{
		SelfHostedRelayPublicHost: "relay.example.com",
	}
	got := ResolveEndpoints(in)
	want := []Endpoint{{Kind: EndpointSelfHostedRelay, URL: "https://relay.example.com"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("self-hosted-only endpoints = %+v, want %+v", got, want)
	}
}

// feature:dual-url-endpoints — determinism: the same inputs with the host
// reporting addresses in a different order yield a byte-identical list.
func TestResolveEndpointsDeterministic(t *testing.T) {
	mk := func(order []string) EndpointInputs {
		addrs := make([]net.Addr, len(order))
		for i, c := range order {
			addrs[i] = ipnet(t, c)
		}
		return EndpointInputs{
			LANScheme: "http", LANPort: 8800,
			interfaces: func() ([]overlayIface, error) {
				return []overlayIface{{name: "en0", addrs: addrs}}, nil
			},
		}
	}
	a := ResolveEndpoints(mk([]string{"192.168.1.20/24", "10.0.0.5/8"}))
	b := ResolveEndpoints(mk([]string{"10.0.0.5/8", "192.168.1.20/24"}))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("non-deterministic order: %+v vs %+v", a, b)
	}
}

// feature:dual-url-endpoints — a zero LAN port (ephemeral ":0" not yet bound)
// omits LAN and overlay URLs but still surfaces direct/relay paths.
func TestResolveEndpointsNoLANPort(t *testing.T) {
	in := EndpointInputs{
		DirectDomain:              "home.example.com",
		DirectTLSActive:           true,
		SelfHostedRelayPublicHost: "relay.example.com",
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{{name: "wg0", addrs: []net.Addr{ipnet(t, "10.7.0.2/32")}}}, nil
		},
	}
	got := ResolveEndpoints(in)
	want := []Endpoint{
		{Kind: EndpointDirect, URL: "https://home.example.com"},
		{Kind: EndpointSelfHostedRelay, URL: "https://relay.example.com"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-LAN-port endpoints = %+v, want %+v", got, want)
	}
}

// feature:dual-url-endpoints — overlay detection: name-based (tailscale0, wg*)
// and CIDR-based (100.64.0.0/10) are independent signals; an IP matching both
// appears once.
func TestOverlayDetection(t *testing.T) {
	ifaces := []overlayIface{
		{name: "en0", addrs: []net.Addr{ipnet(t, "192.168.1.20/24")}},       // neither — excluded
		{name: "tailscale0", addrs: []net.Addr{ipnet(t, "100.100.1.1/32")}}, // name + CIDR, once
		{name: "wg-home", addrs: []net.Addr{ipnet(t, "10.13.13.2/32")}},     // name only
	}
	ips := overlayIPs(ifaces)
	var got []string
	for _, ip := range ips {
		got = append(got, ip.String())
	}
	want := []string{"100.100.1.1", "10.13.13.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overlay IPs = %v, want %v", got, want)
	}
}

// feature:dual-url-endpoints — an IPv6 LAN address is bracketed in the URL.
func TestResolveEndpointsIPv6Bracketed(t *testing.T) {
	in := EndpointInputs{
		LANScheme: "http", LANPort: 8800,
		interfaces: func() ([]overlayIface, error) {
			return []overlayIface{{name: "en0", addrs: []net.Addr{ipnet(t, "fd00::1/64")}}}, nil
		},
	}
	got := ResolveEndpoints(in)
	want := []Endpoint{{Kind: EndpointLAN, URL: "http://[fd00::1]:8800"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IPv6 endpoints = %+v, want %+v", got, want)
	}
}
