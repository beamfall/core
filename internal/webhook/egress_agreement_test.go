package webhook

import (
	"net"
	"net/netip"
	"testing"

	"github.com/beamfall/core/internal/scrape"
)

// TestEgressAgreesWithScraperPredicate pins webhook delivery to the scraper's
// SSRF predicate (internal/scrape/public_routability.go): every address the
// scraper refuses must also be refused for an endpoint not approved for the
// local network, and every address outside the LAN classes must be refused
// for an approved one too. The webhook package cannot import scrape in
// production (ARCHITECTURE §4); the architecture walk skips test files, so the
// comparison runs against scrape directly. The probe table holds one or more
// addresses per scraper special-purpose prefix plus the transition forms.
func TestEgressAgreesWithScraperPredicate(t *testing.T) {
	refusedEverywhere := []string{
		"0.0.0.1", "169.254.169.254", "192.0.0.8", "192.0.2.1", "192.31.196.1",
		"192.52.193.1", "192.88.99.1", "192.175.48.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "224.0.0.251", "240.0.0.1", "255.255.255.255",
		"::", "64:ff9b::a9fe:a9fe", "64:ff9b::808:808", "64:ff9b:1::a00:1",
		"100::1", "100:0:0:1::1", "2001::1", "2001:0:4136:e378::1", "2001:1ff::1",
		"2001:db8::1", "2002:a9fe:a9fe::1", "2620:4f:8000::1", "3fff::1",
		"5f00::1", "fe80::1", "ff02::1",
	}
	lan := []string{
		"10.0.0.1", "100.64.0.1", "127.0.0.1", "172.16.0.1", "192.168.1.10",
		"::1", "fd00::1", "fec0::1", "::ffff:192.168.1.10",
	}
	for _, raw := range append(append([]string{}, refusedEverywhere...), lan...) {
		if scrape.IsPublicRoutableIP(net.ParseIP(raw)) {
			t.Errorf("probe %s is public to the scraper; the table must hold only refused addresses", raw)
		}
		if checkDialAddress(net.JoinHostPort(raw, "80"), false) == nil {
			t.Errorf("unapproved endpoint: dial to %s allowed, but the scraper refuses it", raw)
		}
	}
	for _, raw := range refusedEverywhere {
		if checkDialAddress(net.JoinHostPort(raw, "80"), true) == nil {
			t.Errorf("approved endpoint: dial to %s allowed, want refusal", raw)
		}
	}
	for _, raw := range lan {
		if err := checkDialAddress(net.JoinHostPort(raw, "80"), true); err != nil {
			t.Errorf("approved endpoint: dial to LAN %s refused: %v", raw, err)
		}
	}
	for _, raw := range []string{"8.8.8.8", "93.184.216.34", "2606:4700:4700::1111"} {
		if !scrape.IsPublicRoutableIP(net.ParseIP(raw)) {
			t.Errorf("public probe %s refused by the scraper", raw)
		}
		if classify(netip.MustParseAddr(raw)) != classOther {
			t.Errorf("public probe %s not classOther", raw)
		}
	}
}
