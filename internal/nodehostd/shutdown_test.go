package nodehostd

import (
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/beamfall/core/internal/nodehost"
)

// shutdownPromptBound is the budget for retiring a session leg whose handler is
// parked on a quiet connection. It sits far below handshakeTimeout, because
// waiting out that timeout is the defect under test.
const shutdownPromptBound = 2 * time.Second

// A node that connects and then goes quiet pins its handler for the whole
// handshake timeout. Closing the listener stops accepts but does not touch that
// handler, so without Shutdown the connection — and the goroutine holding it —
// outlives the main's shutdown. Shutdown must retire both promptly.
func TestShutdownRetiresHandlersHoldingAQuietConnection(t *testing.T) {
	server, listener := startShutdownTestServer(t)
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	waitForShutdownTestHandler(t, server)

	_ = listener.Close()
	start := time.Now()
	server.Shutdown()
	elapsed := time.Since(start)

	// Shutdown closes the connection out from under the handler, so it returns
	// promptly. Waiting for the handler's own deadline instead would take the
	// full handshakeTimeout, which is exactly the defect this guards.
	if elapsed >= shutdownPromptBound {
		t.Fatalf("Shutdown took %v, want under %v (the handshake timeout is %v)", elapsed, shutdownPromptBound, handshakeTimeout)
	}
	if live := shutdownTestLiveCount(server); live != 0 {
		t.Fatalf("live connections after Shutdown = %d, want 0", live)
	}
	assertShutdownTestConnClosed(t, conn)
}

// Shutdown is idempotent and safe without a preceding Serve, so a listener that
// never bound cannot make the caller's shutdown path panic or block.
func TestShutdownIsIdempotentAndSafeWithoutServe(t *testing.T) {
	server := NewServer(shutdownTestAdmitter{}, nodehost.CompatibilityPolicy{}, &tls.Config{MinVersion: tls.VersionTLS13}, slog.New(slog.DiscardHandler))
	server.Shutdown()
	server.Shutdown()
}

// A connection accepted after Shutdown must be refused outright rather than
// registered against a handler counter nobody will ever wait on again.
func TestServeRefusesConnectionsAfterShutdown(t *testing.T) {
	server, listener := startShutdownTestServer(t)
	server.Shutdown()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	assertShutdownTestConnClosed(t, conn)
	if live := shutdownTestLiveCount(server); live != 0 {
		t.Fatalf("live connections after a refused accept = %d, want 0", live)
	}
	_ = listener.Close()
}

type shutdownTestAdmitter struct{}

func (shutdownTestAdmitter) OpenSession(nodehost.SessionRequest, nodehost.CompatibilityPolicy) (nodehost.Admission, error) {
	return "", errors.New("nodehostd shutdown test: no session admission")
}

func startShutdownTestServer(t *testing.T) (*Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := NewServer(shutdownTestAdmitter{}, nodehost.CompatibilityPolicy{}, &tls.Config{MinVersion: tls.VersionTLS13}, slog.New(slog.DiscardHandler))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = listener.Close()
		server.Shutdown()
	})
	return server, listener
}

func shutdownTestLiveCount(server *Server) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return len(server.live)
}

func waitForShutdownTestHandler(t *testing.T, server *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if shutdownTestLiveCount(server) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("connection was never registered against the session server")
}

func assertShutdownTestConnClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, 1)
	if _, err := conn.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("read after close = %v, want %v", err, io.EOF)
	}
}
