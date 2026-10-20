package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestUndeclaredProxyForwardedRequestIsNotLAN pins BUGREV0925-CORE-LAN-PROXY-1:
// a request that carries forwarding evidence from a private peer must not read
// as LAN, and its forwarded scheme must not be trusted, when no known proxy is
// declared. Before the fix, requestIsLAN read only r.RemoteAddr and never
// consulted requestWasForwarded, so an undeclared relay's own private address
// (for example a shipped reverse-proxy container's private bridge peer)
// satisfied the LAN check for every client of that relay.
func TestUndeclaredProxyForwardedRequestIsNotLAN(t *testing.T) {
	srv := &Server{} // no known proxies declared
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = "172.20.0.3:4000" // private relay-sidecar peer
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	got := srv.resolveProxyRequest(req)
	if requestIsLAN(got) {
		t.Fatal("undeclared-proxy forwarded request from a private peer classified as LAN")
	}
	if requestIsHTTPS(got) {
		t.Fatal("undeclared-proxy forwarded proto trusted without a declared known proxy")
	}
}

// TestUndeclaredProxyForwardedRequestRejectsPlainHTTPCredentials pins the
// pairing-gate consequence of the check above: rejectPublicPlainHTTPWebCredentials
// must refuse the same request rather than accepting it as an on-LAN login.
func TestUndeclaredProxyForwardedRequestRejectsPlainHTTPCredentials(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = "172.20.0.3:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	got := srv.resolveProxyRequest(req)
	rec := httptest.NewRecorder()
	if !rejectPublicPlainHTTPWebCredentials(rec, got) {
		t.Fatal("expected plain-HTTP web credentials to be refused for an undeclared-proxy forwarded request")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// TestUnforwardedPrivatePeerIsStillLAN is the control: an ordinary same-network
// request that carries no forwarding evidence at all must keep reading as LAN
// even with no known proxies declared.
func TestUnforwardedPrivatePeerIsStillLAN(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = "192.168.1.50:4000"
	got := srv.resolveProxyRequest(req)
	if !requestIsLAN(got) {
		t.Fatal("unforwarded private peer no longer classified as LAN")
	}
}

// TestWebCredentialCookieSecureTracksTrustedForwardedProto closes the acceptance
// clause that the cookie must be Secure when the forwarded proto is https: for a
// request resolved through a declared, allowlisted proxy, the trusted forwarded
// scheme must still produce a Secure cookie.
func TestWebCredentialCookieSecureTracksTrustedForwardedProto(t *testing.T) {
	proxies, err := ParseKnownProxies("10.0.0.0/8", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{knownProxies: proxies}
	req := httptest.NewRequest(http.MethodGet, "http://server.test/health", nil)
	req.RemoteAddr = "10.0.0.2:4000"
	req.Header.Set("X-Forwarded-For", "198.51.100.9")
	req.Header.Set("X-Forwarded-Proto", "https")
	got := srv.resolveProxyRequest(req)

	rec := httptest.NewRecorder()
	setWebCredentialCookie(rec, got, "session", "value", time.Time{})
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("set %d cookies, want 1", len(cookies))
	}
	if !cookies[0].Secure {
		t.Fatal("cookie Secure = false for a trusted forwarded-https request through a declared proxy")
	}
}
