package remoteaccess

import "net"

// relayCarrierUnsentLowat is how many bytes the TLS/TCP relay carrier keeps
// written but not yet sent before its writer waits. It mirrors the relay's
// protocol.CarrierUnsentLowat (beamfall/relay internal/protocol/sendbacklog.go).
// yamux frames every relayed stream onto the one carrier connection in arrival
// order, so bytes the kernel has accepted but not yet put on the wire are a
// queue a playback sync signal cannot overtake. Left to the kernel's
// send-buffer autotuning that queue reaches megabytes; at 16 KiB it drains in
// 64 ms on a 2 Mbit/s uplink. It does not limit throughput: the bytes in
// flight are still governed by the congestion and receive windows (CORE-2282).
const relayCarrierUnsentLowat = 16 << 10

// boundCarrierUnsentBacklog bounds the unsent send backlog of a TCP carrier
// connection to relayCarrierUnsentLowat where the platform can (Linux and
// macOS) and returns the connection to use in its place. Connections that are
// not TCP, and platforms without the control, are returned unchanged. Call it
// on the raw connection, below TLS.
func boundCarrierUnsentBacklog(conn net.Conn) net.Conn {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return conn
	}
	return boundTCPUnsentBacklog(tcp)
}
