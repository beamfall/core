package server

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseKnownProxiesFailsClosed(t *testing.T) {
	for _, value := range []string{"0.0.0.0/0", "10.0.0.0/7", "203.0.113.0/24", "::/0"} {
		if _, err := ParseKnownProxies(value, false); err == nil {
			t.Errorf("ParseKnownProxies(%q) accepted unsafe CIDR", value)
		}
	}
	if _, err := ParseKnownProxies("10.0.0.0/8, 192.168.1.0/24, fc00::/7", false); err != nil {
		t.Fatalf("safe private allowlist rejected: %v", err)
	}
	if _, err := ParseKnownProxies("203.0.113.0/24", true); err != nil {
		t.Fatalf("explicit broad-CIDR escape hatch rejected config: %v", err)
	}
}

func TestKnownProxyResolutionTrustBoundary(t *testing.T) {
	proxies, err := ParseKnownProxies("10.0.0.0/8", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{knownProxies: proxies}

	tests := []struct {
		name       string
		remote     string
		xff        string
		proto      string
		wantRemote string
		wantHTTPS  bool
		trusted    bool
	}{
		{name: "outside CIDR spoof ignored", remote: "203.0.113.8:4000", xff: "192.168.1.2", proto: "https", wantRemote: "203.0.113.8:4000"},
		{name: "single trusted hop", remote: "10.1.2.3:4000", xff: "198.51.100.9", proto: "https", wantRemote: "198.51.100.9:4000", wantHTTPS: true, trusted: true},
		{name: "attacker text prepended", remote: "10.1.2.3:4000", xff: "evil, 198.51.100.9", wantRemote: "198.51.100.9:4000", trusted: true},
		{name: "valid multi hop falls through", remote: "10.1.2.3:4000", xff: "198.51.100.8, 198.51.100.9", proto: "https", wantRemote: "10.1.2.3:4000"},
		{name: "absent header falls through", remote: "10.1.2.3:4000", wantRemote: "10.1.2.3:4000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
			req.RemoteAddr = tt.remote
			req.Header.Set("X-Forwarded-For", tt.xff)
			req.Header.Set("X-Forwarded-Proto", tt.proto)
			got := srv.resolveProxyRequest(req)
			if got.RemoteAddr != tt.wantRemote {
				t.Fatalf("RemoteAddr = %q, want %q", got.RemoteAddr, tt.wantRemote)
			}
			if requestIsHTTPS(got) != tt.wantHTTPS {
				t.Fatalf("requestIsHTTPS = %v, want %v", requestIsHTTPS(got), tt.wantHTTPS)
			}
			resolution, ok := ProxyResolutionFromRequest(got)
			if !ok || resolution.Trusted != tt.trusted || resolution.OriginalAddr != tt.remote {
				t.Fatalf("resolution = %#v, present=%v", resolution, ok)
			}
		})
	}
}

func TestResolvedRemoteClientIsNotLAN(t *testing.T) {
	proxies, _ := ParseKnownProxies("10.0.0.0/8", false)
	srv := &Server{knownProxies: proxies}
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = "10.0.0.2:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	if requestIsLAN(srv.resolveProxyRequest(req)) {
		t.Fatal("resolved remote client classified as LAN")
	}
}

// TestForwardedForPrivateAddressBoundary pins the requestIsLAN boundary for a
// client-supplied X-Forwarded-For carrying a private address (the attack
// direction TestResolvedRemoteClientIsNotLAN doesn't cover: that test only
// checks a public forwarded address). A properly appending proxy
// ($proxy_add_x_forwarded_for) always adds its own resolved hop, producing a
// 2-address chain that singleForwardedAddress rejects outright (it only ever
// trusts exactly one valid address). The bare single-value form is reachable
// only through a proxy misconfigured to pass the client's X-Forwarded-For
// through verbatim — the documented residual risk on requestIsLAN — and this
// test pins that boundary rather than asserting it is safe.
func TestForwardedForPrivateAddressBoundary(t *testing.T) {
	proxies, _ := ParseKnownProxies("10.0.0.0/8", false)
	srv := &Server{knownProxies: proxies}

	t.Run("appended chain rejects the client-supplied private hop", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
		req.RemoteAddr = "10.0.0.2:4000"
		req.Header.Set("X-Forwarded-For", "192.168.1.5, 198.51.100.9")
		resolution, ok := ProxyResolutionFromRequest(srv.resolveProxyRequest(req))
		if !ok || resolution.Trusted {
			t.Fatalf("multi-hop forwarded chain with a client-prepended private address was trusted: %#v", resolution)
		}
	})

	t.Run("bare single-value private address resolves LAN (proxy-misconfiguration boundary)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
		req.RemoteAddr = "10.0.0.2:4000"
		req.Header.Set("X-Forwarded-For", "192.168.1.5")
		if !requestIsLAN(srv.resolveProxyRequest(req)) {
			t.Fatalf("expected bare single-value private XFF to resolve LAN at the documented boundary")
		}
	})
}

func TestForwardedProtoRequiresTrustedResolvedHop(t *testing.T) {
	proxies, _ := ParseKnownProxies("10.0.0.0/8", false)
	srv := &Server{knownProxies: proxies}
	for _, remote := range []string{"203.0.113.8:4000", "10.0.0.2:4000"} {
		req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-Proto", "https")
		if requestIsHTTPS(srv.resolveProxyRequest(req)) {
			t.Fatalf("trusted forwarded proto without trusted address resolution from %s", remote)
		}
	}
	direct := httptest.NewRequest(http.MethodGet, "https://server.test/health", nil)
	direct.TLS = &tls.ConnectionState{}
	if !requestIsHTTPS(direct) {
		t.Fatal("direct TLS no longer classified as HTTPS")
	}
}

func TestBaseURLStripsPrefixAndRewritesGeneratedURL(t *testing.T) {
	base, err := parseBaseURL("https://proxy.example/beamfall/")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		baseURL: base,
		handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				t.Errorf("stripped path = %q, want /", r.URL.Path)
			}
			http.Redirect(w, r, "//attacker.example/web/", http.StatusTemporaryRedirect)
		}),
	}
	req := httptest.NewRequest(http.MethodGet, "http://inbound.invalid/beamfall/", nil)
	req.Host = "poison.example"
	req.Header.Set("X-Forwarded-Host", "forwarded-poison.example")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); got != "https://proxy.example/beamfall/web/" {
		t.Fatalf("Location = %q", got)
	}

	outside := httptest.NewRecorder()
	srv.ServeHTTP(outside, httptest.NewRequest(http.MethodGet, "http://inbound.invalid/web/", nil))
	if outside.Code != http.StatusNotFound {
		t.Fatalf("outside-prefix status = %d, want 404", outside.Code)
	}
}

func TestBaseURLCookiePathUsesPrefix(t *testing.T) {
	base, _ := parseBaseURL("https://proxy.example/beamfall")
	req := httptest.NewRequest(http.MethodGet, "http://server.test/", nil)
	req = req.WithContext(contextWithBaseURL(req, base))
	rec := httptest.NewRecorder()
	setWebCredentialCookie(rec, req, "session", "value", time.Time{})
	if cookie := rec.Header().Get("Set-Cookie"); !strings.Contains(cookie, "Path=/beamfall/") {
		t.Fatalf("Set-Cookie = %q", cookie)
	}
}

func TestWebAssetsUseConfiguredBasePath(t *testing.T) {
	base, _ := parseBaseURL("https://proxy.example/beamfall")
	for _, requestPath := range []string{"/web/", "/web/manifest.webmanifest", "/web/sw.js"} {
		t.Run(requestPath, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://server.test"+requestPath, nil)
			req = req.WithContext(contextWithBaseURL(req, base))
			rec := httptest.NewRecorder()
			webAppHandler(base).ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			if body := rec.Body.String(); strings.Contains(body, "\"/web/") || !strings.Contains(body, "/beamfall/web/") {
				t.Fatalf("asset paths were not base-prefixed: %s", body)
			}
		})
	}
}
