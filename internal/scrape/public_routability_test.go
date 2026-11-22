package scrape

import (
	"net"
	"testing"
)

// TestPublicRoutabilityDeniesDeprecatedSiteLocal pins the deprecated IPv6
// site-local block (fec0::/10, RFC 3879). Go's IsGlobalUnicast reports it as
// global unicast, so only the explicit deny corpus keeps it out of the
// ordinary-global-unicast set; both the predicate and the dial boundary must
// refuse it while ordinary public addresses stay allowed.
func TestPublicRoutabilityDeniesDeprecatedSiteLocal(t *testing.T) {
	denied := []string{"fec0::", "fec0::1", "fed0::1", "feff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"}
	for _, address := range denied {
		if IsPublicRoutableIP(net.ParseIP(address)) {
			t.Errorf("deprecated site-local %s was public-routable", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "93.184.216.34", "2606:4700:4700::1111", "2a00:1450:4001::200e"} {
		if !IsPublicRoutableIP(net.ParseIP(address)) {
			t.Errorf("ordinary global unicast %s was denied", address)
		}
	}

	hardened := New()
	for _, address := range []string{"[fec0::1]:443", "[fefe::abcd]:80"} {
		if err := hardened.controlDial("tcp6", address, nil); err == nil {
			t.Errorf("dial to deprecated site-local %s was allowed, want refusal", address)
		}
	}
	for _, address := range []string{"93.184.216.34:443", "[2606:4700:4700::1111]:443"} {
		if err := hardened.controlDial("tcp", address, nil); err != nil {
			t.Errorf("dial to public %s refused: %v", address, err)
		}
	}
}

// TestPublicRoutabilityDeniesIPv4Compatible pins the deprecated
// IPv4-compatible IPv6 block (::/96). net/netip neither unmaps these forms nor
// treats them as special, so without the explicit deny ::7f00:1 (127.0.0.1)
// passed as public (CORE-1025).
func TestPublicRoutabilityDeniesIPv4Compatible(t *testing.T) {
	denied := map[string]string{
		"::7f00:1":           "loopback 127.0.0.1",
		"::7fff:ffff":        "loopback 127.255.255.255",
		"::a00:1":            "private 10.0.0.1",
		"::ac10:1":           "private 172.16.0.1",
		"::c0a8:10a":         "private 192.168.1.10",
		"::6440:1":           "shared 100.64.0.1",
		"::a9fe:a9fe":        "link-local 169.254.169.254",
		"::808:808":          "public 8.8.8.8 in a deprecated form",
		"::ffff:ffff":        "top of ::/96",
		"::0.0.0.2":          "unspecified-block 0.0.0.2",
		"::1":                "loopback ::1",
		"::":                 "unspecified ::",
		"0:0:0:0:0:0:7f00:1": "uncompressed loopback 127.0.0.1",
	}
	for address, what := range denied {
		ip := net.ParseIP(address)
		if ip == nil {
			t.Fatalf("probe %s did not parse", address)
		}
		if IsPublicRoutableIP(ip) {
			t.Errorf("IPv4-compatible %s (%s) was public-routable", address, what)
		}
	}
	hardened := New()
	for _, address := range []string{"[::7f00:1]:443", "[::a9fe:a9fe]:80", "[::a00:1]:80"} {
		if err := hardened.controlDial("tcp6", address, nil); err == nil {
			t.Errorf("dial to IPv4-compatible %s was allowed, want refusal", address)
		}
	}
}
