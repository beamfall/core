package remoteaccess

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"testing"
)

// feature:self-hosted-relay — only a TCP connection has a send backlog to
// bound; anything else is handed back untouched.
func TestBoundCarrierUnsentBacklogLeavesOtherConnsAlone(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	if got := boundCarrierUnsentBacklog(a); got != a {
		t.Fatalf("a non-TCP connection came back as %T", got)
	}
}

// feature:self-hosted-relay — a carrier dialled through the bound still
// completes TLS 1.3 and carries data both ways, on every platform.
func TestDialBoundedTLSCarriesData(t *testing.T) {
	carrier, accepted := dialTestCarrier(t)
	msg := []byte("carrier frame")
	for _, c := range []struct{ from, to net.Conn }{{carrier, accepted}, {accepted, carrier}} {
		if _, err := c.from.Write(msg); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(c.to, got); err != nil {
			t.Fatal(err)
		}
		if string(got) != string(msg) {
			t.Fatalf("read %q, want %q", got, msg)
		}
	}
}

// dialTestCarrier dials a loopback TLS 1.3 listener with dialBoundedTLS, the
// production carrier dial, and returns the dialled carrier and the accepted
// end. The accepted end is not read unless the test reads it.
func dialTestCarrier(t *testing.T) (carrier *tls.Conn, accepted net.Conn) {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{selfSignedCert(t)},
		MinVersion:   tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	type result struct {
		conn net.Conn
		err  error
	}
	acceptedCh := make(chan result, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr == nil {
			aerr = conn.(*tls.Conn).HandshakeContext(context.Background())
		}
		acceptedCh <- result{conn, aerr}
	}()
	conn, err := dialBoundedTLS(context.Background(), ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: loopback test listener with a self-signed certificate.
		MinVersion:         tls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	res := <-acceptedCh
	if res.err != nil {
		t.Fatal(res.err)
	}
	t.Cleanup(func() { _ = res.conn.Close() })
	carrier, ok := conn.(*tls.Conn)
	if !ok {
		t.Fatalf("dialled carrier is %T", conn)
	}
	return carrier, res.conn
}
