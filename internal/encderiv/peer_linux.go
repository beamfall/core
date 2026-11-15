//go:build linux

package encderiv

import (
	"net/netip"
	"os"
)

// procSocketTables are the kernel socket tables the peer check reads, in order.
var procSocketTables = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// checkPeer refuses a connection unless its client socket is owned by this
// process's user. A table that cannot be read or a socket that cannot be found
// refuses: the check fails closed. Any same-user peer is accepted: a process
// running as the server's user already holds its key access (ADR-0235 D1,
// ADR-0045 §2.2).
func checkPeer(peer, server netip.AddrPort) error {
	for _, name := range procSocketTables {
		file, err := os.Open(name) //nolint:gosec // fixed /proc socket table paths
		if err != nil {
			continue
		}
		uid, found, err := peerUID(file, peer, server)
		_ = file.Close()
		if err != nil {
			return errForbidden
		}
		if found {
			if int64(uid) != int64(os.Getuid()) {
				return errForbidden
			}
			return nil
		}
	}
	return errForbidden
}
