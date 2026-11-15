package encderiv

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

// peerUID finds, in one Linux /proc/net/tcp or /proc/net/tcp6 table, the
// socket whose local address is peer and whose remote address is server (the
// client end of a connection to the endpoint) and returns its owner's uid.
// found is false when no row matches; rows that do not parse cannot match and
// are skipped.
func peerUID(table io.Reader, peer, server netip.AddrPort) (uid uint32, found bool, err error) {
	scanner := bufio.NewScanner(table)
	first := true
	for scanner.Scan() {
		if first {
			// The header row names the columns.
			first = false
			continue
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		local, ok := parseProcAddr(fields[1])
		if !ok || local != peer {
			continue
		}
		remote, ok := parseProcAddr(fields[2])
		if !ok || remote != server {
			continue
		}
		value, err := strconv.ParseUint(fields[7], 10, 32)
		if err != nil {
			continue
		}
		return uint32(value), true, nil
	}
	return 0, false, scanner.Err()
}

// parseProcAddr decodes one ADDR:PORT column. The kernel prints each 32-bit
// word of the address as a host-order integer (one word for IPv4, four for
// IPv6) and the port as plain hex.
func parseProcAddr(field string) (netip.AddrPort, bool) {
	addrHex, portHex, ok := strings.Cut(field, ":")
	if !ok || len(portHex) != 4 {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(raw[i:i+4], binary.BigEndian.Uint32(raw[i:i+4]))
	}
	var addr netip.Addr
	if len(raw) == 4 {
		addr = netip.AddrFrom4([4]byte(raw))
	} else {
		addr = netip.AddrFrom16([16]byte(raw))
	}
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), true
}
