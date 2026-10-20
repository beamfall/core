package scrape

import (
	"net"
	"net/netip"
)

// specialUsePrefixes is the complete IANA IPv4/IPv6 Special-Purpose Address
// registry snapshot updated 2025-10-09. Overlapping, more-specific registry
// rows are represented by their enclosing special-use block because outbound
// fetches deny the entire special-purpose corpus, including globally reachable
// protocol anycasts. Ordinary global unicast outside these blocks is allowed.
var specialUsePrefixes = mustSpecialUsePrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.31.196.0/24", "192.52.193.0/24", "192.88.99.0/24", "192.168.0.0/16",
	"192.175.48.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4",
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48",
	"100::/64", "100:0:0:1::/64", "2001::/23", "2001:db8::/32", "2002::/16",
	"2620:4f:8000::/48", "3fff::/20", "5f00::/16", "fc00::/7", "fe80::/10",
	"ff00::/8",
)

func mustSpecialUsePrefixes(raw ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, len(raw))
	for i, value := range raw {
		prefixes[i] = netip.MustParsePrefix(value)
	}
	return prefixes
}

// IsPublicRoutableIP is the single SSRF predicate used by scraper and
// Integration Worker outbound transports. It accepts only ordinary global
// unicast and rejects IPv4-mapped forms after canonical unmapping.
func IsPublicRoutableIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
