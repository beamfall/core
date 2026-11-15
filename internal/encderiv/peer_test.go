package encderiv

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
)

// procTable is a /proc/net/tcp excerpt as a little-endian kernel prints it: a
// listener on 127.0.0.1:8080, the client socket 127.0.0.1:50000 connected to
// it (owned by uid 1001), and the server's side of that connection (uid 1000).
const procTable = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1F90 0100007F:C350 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:C350 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1001        0 33333 1 0000000000000000 20 4 30 10 -1
   3: garbage
   4: 0100007F:ZZZZ 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  0        0 44444 1
`

// procTable6 is a /proc/net/tcp6 excerpt: an IPv4-mapped client socket and a
// ::1 one.
const procTable6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0000000000000000FFFF00000100007F:C351 0000000000000000FFFF00000100007F:1F90 01 00000000:00000000 00:00000000 00000000  1002        0 55555 1
   1: 00000000000000000000000001000000:C352 00000000000000000000000001000000:1F90 01 00000000:00000000 00:00000000 00000000  1003        0 66666 1
`

func requireLittleEndian(t *testing.T) {
	t.Helper()
	var probe [2]byte
	binary.NativeEndian.PutUint16(probe[:], 1)
	if probe[0] != 1 {
		t.Skip("the literal tables are little-endian kernel output")
	}
}

func TestPeerUIDFindsTheClientSocket(t *testing.T) {
	requireLittleEndian(t)
	server := netip.MustParseAddrPort("127.0.0.1:8080")
	for _, tc := range []struct {
		table  string
		peer   string
		server string
		uid    uint32
		found  bool
	}{
		{procTable, "127.0.0.1:50000", "127.0.0.1:8080", 1001, true},
		{procTable, "127.0.0.1:50001", "127.0.0.1:8080", 0, false},
		{procTable, "127.0.0.1:50000", "127.0.0.1:8081", 0, false},
		{procTable6, "127.0.0.1:50001", "127.0.0.1:8080", 1002, true},
		{procTable6, "[::1]:50002", "[::1]:8080", 1003, true},
		{procTable6, "[::1]:50002", "127.0.0.1:8080", 0, false},
	} {
		peer := netip.MustParseAddrPort(tc.peer)
		uid, found, err := peerUID(strings.NewReader(tc.table), peer, netip.MustParseAddrPort(tc.server))
		if err != nil || found != tc.found || uid != tc.uid {
			t.Fatalf("peer %s: uid %d found %v err %v; want %d %v", tc.peer, uid, found, err, tc.uid, tc.found)
		}
	}
	// The server's own side of the connection is not the peer.
	uid, found, err := peerUID(strings.NewReader(procTable), server, netip.MustParseAddrPort("127.0.0.1:50000"))
	if err != nil || !found || uid != 1000 {
		t.Fatalf("reverse lookup: uid %d found %v err %v", uid, found, err)
	}
}

func TestParseProcAddr(t *testing.T) {
	requireLittleEndian(t)
	for field, want := range map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"00000000:0000":                         "0.0.0.0:0",
		"0000000000000000FFFF00000100007F:C350": "127.0.0.1:50000",
		"00000000000000000000000001000000:0016": "[::1]:22",
	} {
		got, ok := parseProcAddr(field)
		if !ok || got != netip.MustParseAddrPort(want) {
			t.Fatalf("parseProcAddr(%q) = %v, %v; want %s", field, got, ok, want)
		}
	}
	for _, field := range []string{"", "0100007F", "0100007F:1F9", "0100007F:1F900", "0100007G:1F90", "01007F:1F90", "0100007F:ZZZZ"} {
		if _, ok := parseProcAddr(field); ok {
			t.Fatalf("parseProcAddr(%q) accepted a malformed field", field)
		}
	}
}
