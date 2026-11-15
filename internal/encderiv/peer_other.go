//go:build !linux

package encderiv

import "net/netip"

// checkPeer accepts every loopback peer off Linux. The token, the loopback
// bind and the transcoder's sandbox still apply; verifying the peer process on
// macOS is a deferred ADR-0235 item.
func checkPeer(netip.AddrPort, netip.AddrPort) error { return nil }
