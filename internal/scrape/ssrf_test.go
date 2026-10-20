package scrape

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

func TestVetURLRefusals(t *testing.T) {
	hardened := New() // no insecure option: production posture
	cases := []struct {
		name string
		url  string
	}{
		{"plain http", "http://api.invalid/lookup/1"},
		{"http loopback without the option", "http://127.0.0.1:9/lookup/1"},
		{"https literal IPv4", "https://127.0.0.1/lookup/1"},
		{"https literal private IPv4", "https://10.0.0.5/lookup/1"},
		{"https literal IPv6 loopback", "https://[::1]/lookup/1"},
		{"file scheme", "file:///etc/hosts"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hardened.fetch(context.Background(), http.MethodGet, tc.url, nil, "", nil)
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Fatalf("expected refusal for %s, got %v", tc.url, err)
			}
		})
	}
}

func TestDialControlRefusesNonPublicIPs(t *testing.T) {
	hardened := New()
	refused := []string{
		"127.0.0.1:443",         // loopback
		"10.0.0.5:443",          // RFC 1918
		"172.16.4.4:443",        // RFC 1918
		"192.168.1.1:443",       // RFC 1918
		"169.254.169.254:80",    // link-local / cloud metadata
		"0.0.0.0:443",           // unspecified
		"[::1]:443",             // IPv6 loopback
		"[fc00::1]:443",         // IPv6 ULA
		"[fe80::1]:443",         // IPv6 link-local
		"[::ffff:10.0.0.5]:443", // v4-mapped private
	}
	for _, address := range refused {
		if err := hardened.controlDial("tcp", address, nil); err == nil {
			t.Errorf("dial to %s was allowed, want refusal", address)
		}
	}
	allowed := []string{"93.184.216.34:443", "[2606:2800:220:1::1]:443"}
	for _, address := range allowed {
		if err := hardened.controlDial("tcp", address, nil); err != nil {
			t.Errorf("dial to public %s refused: %v", address, err)
		}
	}
	// The test-only option opens loopback and nothing else.
	loopback := New(WithInsecureLoopback())
	if err := loopback.controlDial("tcp", "127.0.0.1:443", nil); err != nil {
		t.Errorf("insecure-loopback engine refused loopback: %v", err)
	}
	if err := loopback.controlDial("tcp", "10.0.0.5:443", nil); err == nil {
		t.Error("insecure-loopback engine allowed a private address")
	}
}

func TestPublicRoutabilityDeniesCompleteSpecialUseCorpus(t *testing.T) {
	for _, prefix := range specialUsePrefixes {
		addresses := []netip.Addr{prefix.Addr()}
		if prefix.Bits() < prefix.Addr().BitLen() {
			addresses = append(addresses, prefix.Addr().Next())
		}
		for _, address := range addresses {
			if IsPublicRoutableIP(net.IP(address.AsSlice())) {
				t.Errorf("special-use address %s from %s was public-routable", address, prefix)
			}
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !IsPublicRoutableIP(net.ParseIP(address)) {
			t.Errorf("ordinary global unicast %s was denied", address)
		}
	}
}

func TestCrossHostRedirectRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.invalid/x", http.StatusFound)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "redirsource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/{id}",
	}, "1")
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("expected cross-origin redirect refusal, got %v", err)
	}
}

func TestSameHostCrossPortRedirectRefused(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("cross-origin destination received Authorization %q", got)
		}
		fmt.Fprint(w, `{"name":"credential leak"}`)
	}))
	defer destination.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/new", http.StatusFound)
	}))
	defer source.Close()

	engine := New(WithInsecureLoopback(), WithCredentials(staticCredentials{"auth": "secret"}))
	_, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "redirsource", Kind: "http-json", Endpoint: source.URL, LookupTemplate: "/old",
		AuthRef: "auth", AuthStyle: "bearer",
	}, "1")
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("same-host cross-port redirect = %v, want refusal", err)
	}
}

func TestSameHostRedirectFollowed(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, server.URL+"/new", http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"name": "Moved"}`)
	}))
	defer server.Close()

	engine := New(WithInsecureLoopback())
	result, err := engine.Lookup(context.Background(), plugin.ScraperSpec{
		ID: "redirsource", Kind: "http-json", Endpoint: server.URL, LookupTemplate: "/old",
		FieldMap: map[string]string{"title": "name"},
	}, "1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Fields["title"] != "Moved" {
		t.Errorf("title = %q", result.Fields["title"])
	}
}
