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
