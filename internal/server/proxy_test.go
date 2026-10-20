package server

import (
	"net/http"
	"net/http/httptest"
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
