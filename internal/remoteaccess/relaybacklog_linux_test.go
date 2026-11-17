package remoteaccess

import (
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

// feature:self-hosted-relay — the dialled carrier's socket, below TLS, has
// TCP_NOTSENT_LOWAT at the relay's value (CORE-2282).
func TestDialBoundedTLSSetsNotsentLowat(t *testing.T) {
	carrier, _ := dialTestCarrier(t)
	tcp, ok := carrier.NetConn().(*net.TCPConn)
	if !ok {
		t.Fatalf("connection below TLS is %T", carrier.NetConn())
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var got int
	var optErr error
	if err := raw.Control(func(fd uintptr) {
		got, optErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT)
	}); err != nil || optErr != nil {
		t.Fatal(err, optErr)
	}
	if got != relayCarrierUnsentLowat {
		t.Errorf("TCP_NOTSENT_LOWAT = %d, want %d", got, relayCarrierUnsentLowat)
	}
}
