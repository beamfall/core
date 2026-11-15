//go:build linux

package encderiv

import (
	"net"
	"net/netip"
	"testing"
)

func TestCheckPeerAcceptsOwnSocketAndRefusesUnknown(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	conn, err := net.Dial("tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = accepted.Close() }()

	peer := conn.LocalAddr().(*net.TCPAddr).AddrPort()
	server := ln.Addr().(*net.TCPAddr).AddrPort()
	if err := checkPeer(peer, server); err != nil {
		t.Fatalf("checkPeer refused this process's own socket: %v", err)
	}
	unknown := netip.AddrPortFrom(peer.Addr(), peer.Port()+1)
	if err := checkPeer(unknown, server); err == nil {
		t.Fatal("checkPeer accepted a socket that is not in the tables")
	}
}
