package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/auth"
)

// TestLANURLsForOffersOnlyTheReachableListener covers CORE-1600: the offered
// address is the connection's own local address, and only when the browser
// sits on that interface's subnet.
func TestLANURLsForOffersOnlyTheReachableListener(t *testing.T) {
	_, home, _ := net.ParseCIDR("192.168.1.0/24")
	_, bridge, _ := net.ParseCIDR("172.17.0.0/16")
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: home.Mask},
		&net.IPNet{IP: net.ParseIP("172.17.0.1"), Mask: bridge.Mask},
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: loop.Mask},
	}
	cases := []struct {
		name   string
		local  net.Addr
		source string
		want   []string
	}{
		{"home subnet", &net.TCPAddr{IP: net.ParseIP("192.168.1.10"), Port: 8800}, "192.168.1.20", []string{"http://192.168.1.10:8800/web/"}},
		{"ipv4-mapped local", &net.TCPAddr{IP: net.ParseIP("::ffff:192.168.1.10"), Port: 8800}, "::ffff:192.168.1.20", []string{"http://192.168.1.10:8800/web/"}},
		{"loopback", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8800}, "127.0.0.1", []string{"http://127.0.0.1:8800/web/"}},
		{"other subnet", &net.TCPAddr{IP: net.ParseIP("172.17.0.1"), Port: 8800}, "192.168.1.20", nil},
		{"unknown interface", &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 8800}, "10.0.0.6", nil},
		{"unspecified", &net.TCPAddr{IP: net.IPv4zero, Port: 8800}, "192.168.1.20", nil},
		{"link-local", &net.TCPAddr{IP: net.ParseIP("169.254.3.4"), Port: 8800}, "169.254.3.5", nil},
		{"no local address", nil, "192.168.1.20", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lanURLsFor(tc.local, net.ParseIP(tc.source), addrs); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lanURLsFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return raw
}

func withLocalAddr(req *http.Request, local string) *http.Request {
	addr, err := net.ResolveTCPAddr("tcp", local)
	if err != nil {
		panic(err)
	}
	return req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, net.Addr(addr)))
}

// TestRebindableAdminSignInRefusalCarriesLANCode proves the CORE-1106 refusal
// still happens for a rebindable Host and now explains itself to LAN browsers
// with a stable code and the server's own address, never to public sources.
func TestRebindableAdminSignInRefusalCarriesLANCode(t *testing.T) {
	srv, _ := newAdminSignInServer(t, "correct horse battery")
	body := `{"password":"correct horse battery","deviceName":"Laptop"}`

	t.Run("loopback source names the accepted listener", func(t *testing.T) {
		req := newJSONRequest(http.MethodPost, "/api/web/admin-signin", strings.NewReader(body))
		req.Header.Set(auth.CSRFHeader, "1")
		req.RemoteAddr = "127.0.0.1:51000"
		req.Host = "beamfall.example:8800"
		req = withLocalAddr(req, "127.0.0.1:8800")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
		raw := decodeBody(t, rec)
		if string(raw["code"]) != `"lan_hostname_not_allowed"` {
			t.Fatalf("code = %s, want lan_hostname_not_allowed", raw["code"])
		}
		var urls []string
		if err := json.Unmarshal(raw["lanUrls"], &urls); err != nil || !reflect.DeepEqual(urls, []string{"http://127.0.0.1:8800/web/"}) {
			t.Fatalf("lanUrls = %s, want the loopback listener", raw["lanUrls"])
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("refusal set cookies: %v", rec.Result().Cookies())
		}
	})

	t.Run("LAN source without a known listener gets an empty list", func(t *testing.T) {
		rec := postAdminSignIn(srv, "192.168.4.20:51000", "attacker.example", body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
		raw := decodeBody(t, rec)
		if string(raw["code"]) != `"lan_hostname_not_allowed"` || string(raw["lanUrls"]) != "[]" {
			t.Fatalf("body = %s, want the code with an empty lanUrls", rec.Body.String())
		}
	})

	t.Run("public source keeps the plain refusal", func(t *testing.T) {
		rec := postAdminSignIn(srv, "203.0.113.9:51000", "attacker.example", body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
		}
		raw := decodeBody(t, rec)
		if _, ok := raw["code"]; ok {
			t.Fatalf("public refusal carries a code: %s", rec.Body.String())
		}
		if _, ok := raw["lanUrls"]; ok {
			t.Fatalf("public refusal carries lanUrls: %s", rec.Body.String())
		}
	})
}

// TestSetupProbeOffersLANURLsOnlyToLANBrowsers covers the optional lanUrls on
// GET /api/setup: present for a LAN browser that still needs the token
// because it addressed a hostname, absent for public sources and once a LAN
// claim is possible.
func TestSetupProbeOffersLANURLsOnlyToLANBrowsers(t *testing.T) {
	srv := newTestServer(t)
	srv.pairing.SetBootstrapToken("first-run-token")
	probe := func(remoteAddr, host string) map[string]json.RawMessage {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
		req.RemoteAddr = remoteAddr
		req.Host = host
		req = withLocalAddr(req, "127.0.0.1:8800")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		return decodeBody(t, rec)
	}
	raw := probe("127.0.0.1:51000", "beamfall.example:8800")
	if string(raw["tokenRequired"]) != "true" || string(raw["lanUrls"]) != `["http://127.0.0.1:8800/web/"]` {
		t.Fatalf("rebindable LAN probe = %v, want tokenRequired with the loopback lanUrls", raw)
	}
	if raw := probe("203.0.113.9:51000", "beamfall.example:8800"); raw["lanUrls"] != nil {
		t.Fatalf("public probe carries lanUrls: %v", raw)
	}
	if raw := probe("127.0.0.1:51000", "127.0.0.1:8800"); raw["lanUrls"] != nil {
		t.Fatalf("claimable LAN probe carries lanUrls: %v", raw)
	}
}

// TestSetupRefusedByHostnameRuleCarriesLANCode covers CORE-1600 on POST
// /api/setup: a LAN browser whose only fault is the missing setup token (it
// addressed a rebindable hostname, so it has no LAN claim) gets the probe's
// code and addresses. The status stays 400 and nothing is created; any other
// fault, a public source or a finished setup keeps the plain refusal.
func TestSetupRefusedByHostnameRuleCarriesLANCode(t *testing.T) {
	post := func(srv *Server, remoteAddr, host, body string, overTLS bool) *httptest.ResponseRecorder {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/setup", strings.NewReader(body))
		req.Header.Set(auth.CSRFHeader, "1")
		req.RemoteAddr = remoteAddr
		req.Host = host
		if overTLS {
			req.TLS = &tls.ConnectionState{}
		}
		req = withLocalAddr(req, "127.0.0.1:8800")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	plainRefusal := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
		raw := decodeBody(t, rec)
		if _, ok := raw["code"]; ok {
			t.Fatalf("refusal carries a code: %s", rec.Body.String())
		}
		if _, ok := raw["lanUrls"]; ok {
			t.Fatalf("refusal carries lanUrls: %s", rec.Body.String())
		}
	}
	const noToken = `{"deviceName":"Laptop","profileName":"Owner"}`

	t.Run("LAN source at a hostname gets the code and the listener", func(t *testing.T) {
		srv := newTestServer(t)
		srv.pairing.SetBootstrapToken("first-run-token")
		rec := post(srv, "127.0.0.1:51000", "beamfall.example:8800", noToken, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
		}
		raw := decodeBody(t, rec)
		if string(raw["code"]) != `"lan_hostname_not_allowed"` || string(raw["lanUrls"]) != `["http://127.0.0.1:8800/web/"]` {
			t.Fatalf("body = %s, want the code with the loopback lanUrls", rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("refusal set cookies: %v", rec.Result().Cookies())
		}
		if !srv.pairing.SetupRequired() {
			t.Fatal("refused setup consumed the setup token")
		}
	})

	t.Run("another fault keeps the plain refusal", func(t *testing.T) {
		srv := newTestServer(t)
		srv.pairing.SetBootstrapToken("first-run-token")
		plainRefusal(t, post(srv, "127.0.0.1:51000", "beamfall.example:8800", `{"profileName":"Owner"}`, false))
	})

	t.Run("public source keeps the plain refusal", func(t *testing.T) {
		srv := newTestServer(t)
		srv.pairing.SetBootstrapToken("first-run-token")
		plainRefusal(t, post(srv, "203.0.113.9:51000", "beamfall.example", noToken, true))
	})

	t.Run("finished setup keeps the plain refusal", func(t *testing.T) {
		srv := newTestServer(t)
		plainRefusal(t, post(srv, "127.0.0.1:51000", "beamfall.example:8800", noToken, false))
	})
}
