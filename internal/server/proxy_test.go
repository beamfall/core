package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loopbackProxyServer declares the loopback range as the reverse-proxy
// allowlist. That is the same-host topology GCR-012-H is about: the proxy and
// the server share a host, so every request the proxy relays arrives with a
// loopback peer address that is indistinguishable from a local client's.
func loopbackProxyServer(t *testing.T) *Server {
	t.Helper()
	proxies, err := ParseKnownProxies("127.0.0.0/8", false)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{knownProxies: proxies}
}

func forwardedRequest(remoteAddr string, header http.Header) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = remoteAddr
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	return req
}

// TestForwardedResolutionFailsTowardLeastPrivilege pins the direction an
// ambiguous forwarded address resolves in. A declared proxy that cannot be
// reduced to exactly one client address resolves no client at all, so the
// address left on the request is the proxy's own — here a loopback address.
// Reading it as the client's proximity would hand an internet client the
// standing of a host-local one, the most privileged reading of an ambiguous
// input. The unresolved cases below must therefore read as non-LAN, while the
// resolved and unproxied controls must keep reading exactly as before.
func TestForwardedResolutionFailsTowardLeastPrivilege(t *testing.T) {
	srv := loopbackProxyServer(t)

	tests := []struct {
		name        string
		forwardedIP []string
		wantTrusted bool
		wantRemote  string
		wantLAN     bool
	}{
		{
			name:       "no forwarding evidence keeps the peer as the client",
			wantRemote: "127.0.0.1:4000",
			wantLAN:    true,
		},
		{
			name:        "one public address resolves the real client",
			forwardedIP: []string{"198.51.100.9"},
			wantTrusted: true,
			wantRemote:  "198.51.100.9:4000",
		},
		{
			name:        "one private address resolves the real client",
			forwardedIP: []string{"192.168.1.5"},
			wantTrusted: true,
			wantRemote:  "192.168.1.5:4000",
			wantLAN:     true,
		},
		{
			name:        "repeated header resolves no client",
			forwardedIP: []string{"198.51.100.9", "203.0.113.7"},
			wantRemote:  "127.0.0.1:4000",
		},
		{
			name:        "multi-value chain resolves no client",
			forwardedIP: []string{"198.51.100.8, 198.51.100.9"},
			wantRemote:  "127.0.0.1:4000",
		},
		{
			name:        "unparseable value resolves no client",
			forwardedIP: []string{"not-an-address"},
			wantRemote:  "127.0.0.1:4000",
		},
		{
			// Direction changed from the round-1 assertion, which expected LAN
			// standing here. A declared proxy relaying a blank field-value
			// resolves no client, so the address left on the request is the
			// proxy's own loopback address; reading it as the client's proximity
			// is the most privileged reading of an ambiguous input — the same
			// defect the rest of this table pins shut.
			name:        "empty value resolves no client",
			forwardedIP: []string{""},
			wantRemote:  "127.0.0.1:4000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := srv.resolveProxyRequest(forwardedRequest("127.0.0.1:4000", http.Header{"X-Forwarded-For": tt.forwardedIP}))
			resolution, ok := ProxyResolutionFromRequest(got)
			if !ok {
				t.Fatal("no proxy resolution recorded for a peer inside the allowlist")
			}
			if !resolution.ViaKnownProxy {
				t.Fatalf("resolution = %#v, want ViaKnownProxy for a peer inside the allowlist", resolution)
			}
			if resolution.Trusted != tt.wantTrusted {
				t.Fatalf("resolution.Trusted = %v, want %v (%#v)", resolution.Trusted, tt.wantTrusted, resolution)
			}
			if got.RemoteAddr != tt.wantRemote {
				t.Fatalf("RemoteAddr = %q, want %q", got.RemoteAddr, tt.wantRemote)
			}
			if requestIsLAN(got) != tt.wantLAN {
				t.Fatalf("requestIsLAN = %v, want %v", requestIsLAN(got), tt.wantLAN)
			}
		})
	}
}

// TestRequestWasForwardedReadsEveryHopHeader keeps each hop header in
// forwardedHeaders load-bearing: dropping any one of them from the list fails
// its own subtest. The empty-value case pins that a header carrying no hop is
// not evidence, which is what keeps a header explicitly set to "" from denying
// a local client.
func TestRequestWasForwardedReadsEveryHopHeader(t *testing.T) {
	values := map[string]string{
		"Forwarded":         "for=203.0.113.7",
		"X-Forwarded-For":   "203.0.113.7",
		"X-Forwarded-Host":  "beamfall.example",
		"X-Forwarded-Port":  "443",
		"X-Forwarded-Proto": "https",
		"X-Real-Ip":         "203.0.113.7",
		"Via":               "1.1 gateway",
	}
	if len(values) != len(forwardedHeaders) {
		t.Fatalf("forwardedHeaders has %d entries, coverage table has %d", len(forwardedHeaders), len(values))
	}
	for _, name := range forwardedHeaders {
		value, ok := values[name]
		if !ok {
			t.Fatalf("hop header %q has no coverage entry", name)
		}
		t.Run(name, func(t *testing.T) {
			if !requestWasForwarded(forwardedRequest("127.0.0.1:4000", http.Header{name: {value}})) {
				t.Fatalf("%s: %q was not read as forwarding evidence", name, value)
			}
			if requestWasForwarded(forwardedRequest("127.0.0.1:4000", http.Header{name: {"  "}})) {
				t.Fatalf("%s with a blank value was read as forwarding evidence", name)
			}
		})
	}
	if requestWasForwarded(forwardedRequest("127.0.0.1:4000", nil)) {
		t.Fatal("a request with no hop headers was read as forwarding evidence")
	}
}

// TestHopHeaderPresenceIsNotHopHeaderEvidence keeps the two hop-header
// questions distinct for every header in the list. A blank field-value is not
// evidence of a hop — that is what keeps a header explicitly set to "" from
// denying a client on this host — but it is still presence, which is the
// question a proximity carve-out has to ask.
func TestHopHeaderPresenceIsNotHopHeaderEvidence(t *testing.T) {
	for _, name := range forwardedHeaders {
		t.Run(name, func(t *testing.T) {
			blank := forwardedRequest("127.0.0.1:4000", http.Header{name: {""}})
			if requestWasForwarded(blank) {
				t.Fatalf("%s: a blank value was read as forwarding evidence", name)
			}
			if !requestCarriesForwardedHeader(blank) {
				t.Fatalf("%s: a blank value was not read as header presence", name)
			}
		})
	}
	if requestCarriesForwardedHeader(forwardedRequest("127.0.0.1:4000", nil)) {
		t.Fatal("a request with no hop headers was read as carrying one")
	}
}

// TestBlankHopHeaderDoesNotInheritProxyProximity pins the fix for the case a
// declared proxy relaying a blank hop header used to win: it resolves no client
// address, so the address left on the request is the proxy's own, and reading
// it as the client's proximity would hand an off-host client the LAN standing
// that gates the ADR-0035 conductor path. The final assertion is the other
// direction — the no-hop-header carve-out that keeps a client which really is
// on this host, inside a declared CIDR, working.
func TestBlankHopHeaderDoesNotInheritProxyProximity(t *testing.T) {
	srv := loopbackProxyServer(t)
	for _, name := range forwardedHeaders {
		t.Run(name, func(t *testing.T) {
			got := srv.resolveProxyRequest(forwardedRequest("127.0.0.1:4000", http.Header{name: {""}}))
			if requestIsLAN(got) {
				t.Fatalf("%s: a declared proxy relaying a blank value kept LAN standing", name)
			}
		})
	}
	unproxied := srv.resolveProxyRequest(forwardedRequest("127.0.0.1:4000", nil))
	if !requestIsLAN(unproxied) {
		t.Fatal("a client inside the declared CIDR sending no hop header lost LAN standing")
	}
}

// TestParseKnownProxiesAcceptsBareAddresses pins the documented entry shape:
// every BEAMFALL_KNOWN_PROXIES entry is an address or a CIDR. A bare address is
// the single-host prefix for that address, goes through the same safety check
// as a CIDR, and dedupes against the equivalent single-host CIDR.
func TestParseKnownProxiesAcceptsBareAddresses(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		allowBroad bool
		want       string
	}{
		{name: "bare IPv4 is a /32", value: "172.28.0.2", want: "172.28.0.2/32"},
		{name: "bare IPv6 is a /128", value: "fd00::2", want: "fd00::2/128"},
		{name: "IPv4-mapped IPv6 unmaps to IPv4", value: "::ffff:10.0.0.5", want: "10.0.0.5/32"},
		{name: "mixed bare and CIDR", value: "172.28.0.2, 10.0.0.0/8", want: "10.0.0.0/8,172.28.0.2/32"},
		{name: "bare and single-host CIDR collapse", value: "10.0.0.5, 10.0.0.5/32, ::ffff:10.0.0.5", want: "10.0.0.5/32"},
		{name: "public bare address with broad escape hatch", value: "8.8.8.8", allowBroad: true, want: "8.8.8.8/32"},
		{name: "existing CIDR parses as before", value: "10.1.2.3/8, 192.168.1.0/24, fc00::/7", want: "10.0.0.0/8,192.168.1.0/24,fc00::/7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxies, err := ParseKnownProxies(tt.value, tt.allowBroad)
			if err != nil {
				t.Fatalf("ParseKnownProxies(%q) = %v", tt.value, err)
			}
			if got := proxies.String(); got != tt.want {
				t.Fatalf("ParseKnownProxies(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseKnownProxiesRejectsBadEntries(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantEntry string
		wantText  string
	}{
		{name: "public bare address without escape hatch", value: "8.8.8.8", wantText: "public or over-broad"},
		{name: "public mapped address without escape hatch", value: "::ffff:8.8.8.8", wantText: "public or over-broad"},
		{name: "garbage", value: "10.0.0.2, not-an-ip", wantEntry: `"not-an-ip"`, wantText: "must be an address or CIDR"},
		{name: "bad CIDR", value: "10.0.0.0/99", wantEntry: `"10.0.0.0/99"`, wantText: "must be an address or CIDR"},
		{name: "address with port", value: "10.0.0.2:8080", wantEntry: `"10.0.0.2:8080"`, wantText: "must be an address or CIDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseKnownProxies(tt.value, false)
			if err == nil {
				t.Fatalf("ParseKnownProxies(%q) accepted a bad entry", tt.value)
			}
			if !strings.Contains(err.Error(), tt.wantEntry) || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ParseKnownProxies(%q) error = %q, want it to name %s and say %q", tt.value, err, tt.wantEntry, tt.wantText)
			}
		})
	}
}

// TestBareKnownProxyResolvesForwardedHeaders proves a proxy declared by bare
// address is trusted: a request whose peer is that address resolves the client
// from its forwarding headers, while a neighbouring peer does not.
func TestBareKnownProxyResolvesForwardedHeaders(t *testing.T) {
	proxies, err := ParseKnownProxies("172.28.0.2", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{knownProxies: proxies}

	got := srv.resolveProxyRequest(forwardedRequest("172.28.0.2:4000", http.Header{
		"X-Forwarded-For":   {"198.51.100.9"},
		"X-Forwarded-Proto": {"https"},
	}))
	if got.RemoteAddr != "198.51.100.9:4000" || !requestIsHTTPS(got) {
		t.Fatalf("bare proxy peer: RemoteAddr = %q, https = %v; want the forwarded client over https", got.RemoteAddr, requestIsHTTPS(got))
	}
	if resolution, ok := ProxyResolutionFromRequest(got); !ok || !resolution.Trusted {
		t.Fatalf("bare proxy peer: resolution = %#v, present = %v; want trusted", resolution, ok)
	}

	neighbour := srv.resolveProxyRequest(forwardedRequest("172.28.0.3:4000", http.Header{
		"X-Forwarded-For": {"198.51.100.9"},
	}))
	if neighbour.RemoteAddr != "172.28.0.3:4000" {
		t.Fatalf("undeclared neighbour: RemoteAddr = %q, want the peer kept", neighbour.RemoteAddr)
	}
}
