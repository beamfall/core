package remoteaccess

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type acceptResult struct {
	conn net.Conn
	err  error
}

type scriptedListener struct {
	results chan acceptResult
	closed  chan struct{}
	once    sync.Once
}

func newScriptedListener(capacity int) *scriptedListener {
	return &scriptedListener{
		results: make(chan acceptResult, capacity),
		closed:  make(chan struct{}),
	}
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	select {
	case result := <-l.results:
		return result.conn, result.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *scriptedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *scriptedListener) Addr() net.Addr { return &net.TCPAddr{} }

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept failure" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

type closeCountingConn struct {
	net.Conn
	closed *atomic.Int32
	once   sync.Once
}

func (c *closeCountingConn) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}

// muxHarness binds a loopback mux listener over the persistent self-signed
// identity and returns the concrete type so tests can join its goroutines.
func muxHarness(t *testing.T) (*tlsMuxListener, string) {
	t.Helper()
	id, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "tls"))
	if err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln, ok := NewTLSMuxListener(raw, id.TLSConfig()).(*tlsMuxListener)
	if !ok {
		t.Fatal("NewTLSMuxListener did not return the concrete mux type")
	}
	return ln, raw.Addr().String()
}

// joinMux fails the test if the mux's accept pump and classification
// goroutines have not all exited within the deadline — the leak check.
func joinMux(t *testing.T, ln *tlsMuxListener, deadline time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ln.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatal("mux goroutines still running after Close: goroutine leak")
	}
}

// feature:dual-url-endpoints
// — a transient inner Accept error is retried instead of retiring the mux.
func TestTLSMuxRetriesTemporaryAcceptError(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "reported temporary", err: temporaryAcceptError{}},
		{name: "connection aborted", err: &net.OpError{Op: "accept", Err: syscall.ECONNABORTED}},
		{name: "connection reset", err: &net.OpError{Op: "accept", Err: syscall.ECONNRESET}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := newScriptedListener(2)
			inner.results <- acceptResult{err: tt.err}
			client, server := net.Pipe()
			defer client.Close()
			inner.results <- acceptResult{conn: server}

			ln := NewTLSMuxListener(inner, &tls.Config{MinVersion: tls.VersionTLS12}).(*tlsMuxListener)
			defer func() {
				_ = ln.Close()
				joinMux(t, ln, time.Second)
			}()
			go func() { _, _ = client.Write([]byte("G")) }()

			conn, err := ln.Accept()
			if err != nil {
				t.Fatalf("Accept after %v = %v, want connection", tt.err, err)
			}
			_ = conn.Close()
		})
	}
}

func TestTLSMuxTransientAcceptErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout", err: os.ErrDeadlineExceeded, want: true},
		{name: "interrupted", err: &net.OpError{Op: "accept", Err: syscall.EINTR}, want: true},
		{name: "process fd exhaustion", err: &net.OpError{Op: "accept", Err: syscall.EMFILE}, want: true},
		{name: "system fd exhaustion", err: &net.OpError{Op: "accept", Err: syscall.ENFILE}, want: true},
		{name: "reported temporary", err: temporaryAcceptError{}, want: true},
		{name: "connection aborted", err: &net.OpError{Op: "accept", Err: syscall.ECONNABORTED}, want: true},
		{name: "connection reset", err: &net.OpError{Op: "accept", Err: syscall.ECONNRESET}, want: true},
		{name: "closed", err: net.ErrClosed, want: false},
		{name: "permanent", err: errors.New("permanent accept failure"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientAcceptError(tt.err); got != tt.want {
				t.Fatalf("isTransientAcceptError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

// feature:dual-url-endpoints
// — a permanent inner Accept error is terminal and remains stable until Close
// changes the public result to net.ErrClosed.
func TestTLSMuxPermanentAcceptErrorTerminates(t *testing.T) {
	permanent := errors.New("permanent accept failure")
	inner := newScriptedListener(1)
	inner.results <- acceptResult{err: permanent}
	ln := NewTLSMuxListener(inner, &tls.Config{MinVersion: tls.VersionTLS12}).(*tlsMuxListener)
	t.Cleanup(func() {
		_ = ln.Close()
		joinMux(t, ln, time.Second)
	})

	if _, err := ln.Accept(); !errors.Is(err, permanent) {
		t.Fatalf("Accept error = %v, want %v", err, permanent)
	}
	joinMux(t, ln, time.Second)
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
	}
}

// feature:dual-url-endpoints
// — silent connections cannot grow classification work beyond the named cap;
// overflow is refused and closed instead of waiting in an unbounded queue.
func TestTLSMuxBoundsInFlightClassifications(t *testing.T) {
	const stormSize = tlsMuxMaxInFlightClassifications + 8
	inner := newScriptedListener(stormSize)
	clients := make([]net.Conn, 0, stormSize)
	var closed atomic.Int32
	for range stormSize {
		client, server := net.Pipe()
		clients = append(clients, client)
		inner.results <- acceptResult{conn: &closeCountingConn{Conn: server, closed: &closed}}
	}
	defer func() {
		for _, client := range clients {
			_ = client.Close()
		}
	}()

	ln := NewTLSMuxListener(inner, &tls.Config{MinVersion: tls.VersionTLS12}).(*tlsMuxListener)
	t.Cleanup(func() {
		_ = ln.Close()
		joinMux(t, ln, time.Second)
	})
	waitUntil(t, time.Second, func() bool {
		ln.mu.Lock()
		defer ln.mu.Unlock()
		return len(ln.pending)+int(closed.Load()) == stormSize
	})

	ln.mu.Lock()
	inFlight := len(ln.pending)
	ln.mu.Unlock()
	if inFlight != tlsMuxMaxInFlightClassifications {
		t.Fatalf("in-flight classifications = %d, want cap %d", inFlight, tlsMuxMaxInFlightClassifications)
	}
	if got, want := int(closed.Load()), stormSize-tlsMuxMaxInFlightClassifications; got != want {
		t.Fatalf("refused-and-closed connections = %d, want %d", got, want)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	joinMux(t, ln, time.Second)
}

// feature:dual-url-endpoints (AUDITFIX-TLS-MUX) — Close is a hard fence: a
// stalled client sitting mid-classification (connected, zero bytes sent) is
// closed by Close IMMEDIATELY, not after the peek timeout, Close returns
// promptly, Accept refuses with net.ErrClosed, and no mux goroutine survives.
func TestTLSMuxCloseInterruptsStalledClassification(t *testing.T) {
	ln, addr := muxHarness(t)

	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	// Wait until the mux owns the connection (it is pending classification).
	waitUntil(t, time.Second, func() bool {
		ln.mu.Lock()
		defer ln.mu.Unlock()
		return len(ln.pending) == 1
	})

	closeStart := time.Now()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(closeStart); elapsed > time.Second {
		t.Fatalf("Close took %v; must not wait out the %v peek timeout", elapsed, tlsMuxPeekTimeout)
	}

	// The stalled client's connection was closed under it, deterministically.
	_ = stalled.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := stalled.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("stalled client still connected after Close (read: %v): connection stranded", err)
	}

	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
	}
	if err := ln.Close(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("second Close = %v, want net.ErrClosed", err)
	}
	joinMux(t, ln, 2*time.Second)
}

// feature:dual-url-endpoints (AUDITFIX-TLS-MUX) — a stalled client must not
// delay anyone else: while one connection sits silent in classification, a
// cleartext request on the same port completes well inside the peek timeout.
// The prior implementation peeked serially inside Accept, so this request
// waited the full 5s behind the silent connection.
func TestTLSMuxStalledClientDoesNotDelayOthers(t *testing.T) {
	ln, addr := muxHarness(t)
	defer ln.Close()

	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go srv.Serve(ln)
	defer srv.Close()

	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	waitUntil(t, time.Second, func() bool {
		ln.mu.Lock()
		defer ln.mu.Unlock()
		return len(ln.pending) >= 1
	})

	client := &http.Client{Timeout: 2 * time.Second} // well under tlsMuxPeekTimeout
	res, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("request behind a stalled client did not complete: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

// feature:dual-url-endpoints (AUDITFIX-TLS-MUX) — accept/close race: under a
// storm of concurrent TLS, cleartext, and silent dials, Close at a random point
// leaves no connection stranded. Every connection the mux ever accepted is
// either handed out by Accept before it reports closed, or closed by the mux;
// after Close, Accept returns net.ErrClosed forever and no goroutine leaks.
// Run with -race: the storm exercises admit/deliver/abort against Close.
func TestTLSMuxAcceptCloseRace(t *testing.T) {
	ln, addr := muxHarness(t)

	const dialers = 24
	var wg sync.WaitGroup
	for i := 0; i < dialers; i++ {
		wg.Add(1)
		go func(kind int) {
			defer wg.Done()
			c, err := net.Dial("tcp", addr)
			if err != nil {
				return // Close already tore the port down — fine.
			}
			defer c.Close()
			switch kind % 3 {
			case 0: // cleartext opener
				_, _ = c.Write([]byte("GET / HTTP/1.1\r\n"))
			case 1: // TLS opener (client handshake will not complete; the
				// first record byte is all classification needs)
				tc := tls.Client(c, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) // #nosec G402 -- the test dials its own self-signed identity; verification is what the reload-fence assertion covers.
				_ = tc.SetDeadline(time.Now().Add(500 * time.Millisecond))
				_ = tc.Handshake()
			case 2: // silent — stays in classification until closed
			}
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			_, _ = c.Read(make([]byte, 1))
		}(i)
	}

	accepted := make(chan net.Conn, dialers)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, err := ln.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Errorf("Accept ended with %v, want net.ErrClosed", err)
				}
				return
			}
			accepted <- c
		}
	}()

	time.Sleep(50 * time.Millisecond) // let the storm land mid-flight
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The fence holds: the acceptor sees ErrClosed and stops promptly.
	select {
	case <-acceptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Accept loop still running after Close")
	}
	// Anything Accept handed out before the fence is the caller's to close.
	close(accepted)
	for c := range accepted {
		_ = c.Close()
	}
	if _, err := ln.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", err)
	}
	joinMux(t, ln, 3*time.Second)
	wg.Wait()
}

// feature:dual-url-endpoints (AUDITFIX-TLS-MUX) — a connection that finishes
// classification while nothing is accepting is still owned by the mux; Close
// retires it rather than leaving it queued forever. The client observes the
// close instead of a stranded half-open connection.
func TestTLSMuxCloseRetiresQueuedConnections(t *testing.T) {
	ln, addr := muxHarness(t)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("GET / HTTP/1.1\r\n")); err != nil {
		t.Fatal(err)
	}
	// No Accept call: the classified connection parks in the queue.
	waitUntil(t, time.Second, func() bool {
		ln.mu.Lock()
		defer ln.mu.Unlock()
		return len(ln.queue) == 1
	})

	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("queued connection still open after Close (read: %v): connection stranded", err)
	}
	joinMux(t, ln, 2*time.Second)
}

// feature:dual-url-endpoints (AUDITFIX-TLS-MUX) — reload fencing: a listener
// restart (Close, then rebind the SAME port under a rotated identity) leaves no
// residue. The predecessor's stalled connection is dead before the successor
// binds, and a fresh TLS dial handshakes against the successor's certificate —
// a leftover connection can never be served from the retired config.
func TestTLSMuxReloadFencesPredecessor(t *testing.T) {
	oldID, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "old"))
	if err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}
	newID, err := LoadOrCreateSelfSigned(filepath.Join(t.TempDir(), "new"))
	if err != nil {
		t.Fatalf("LoadOrCreateSelfSigned: %v", err)
	}

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := raw.Addr().String()
	oldLn := NewTLSMuxListener(raw, oldID.TLSConfig()).(*tlsMuxListener)

	stalled, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	waitUntil(t, time.Second, func() bool {
		oldLn.mu.Lock()
		defer oldLn.mu.Unlock()
		return len(oldLn.pending) == 1
	})

	if err := oldLn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	joinMux(t, oldLn, 2*time.Second)

	// Rebind the same port under the rotated identity — the reload.
	raw2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("rebinding %s after Close: %v", addr, err)
	}
	newLn := NewTLSMuxListener(raw2, newID.TLSConfig())
	defer newLn.Close()
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})}
	go srv.Serve(newLn)
	defer srv.Close()

	// The predecessor's connection died with the predecessor.
	_ = stalled.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := stalled.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("predecessor's connection survived the reload (read: %v)", err)
	}

	// A fresh dial handshakes against the successor's certificate only.
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) // #nosec G402 -- the test dials its own self-signed identity; the assertion is about which cert is served, not chain trust.
	if err != nil {
		t.Fatalf("TLS dial after reload: %v", err)
	}
	defer conn.Close()
	servedLeaf := conn.ConnectionState().PeerCertificates[0].Raw
	newLeaf := newID.TLSConfig().Certificates[0]
	if string(servedLeaf) != string(newLeaf.Certificate[0]) {
		t.Fatal("reloaded listener served a certificate other than the rotated identity's")
	}
}

// waitUntil polls cond until it holds or the deadline lapses.
func waitUntil(t *testing.T, deadline time.Duration, cond func() bool) {
	t.Helper()
	stop := time.Now().Add(deadline)
	for time.Now().Before(stop) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached before deadline")
}

// ---------------------------------------------------------------------------
// Evidence — AUDITFIX-TLS-MUX (2026-08-20)
//
// Builder-lane verification for the lane at
// .roadmap-worktrees/beamfall/AUDITFIX-TLS-MUX. The full repo gate is the
// orchestrator's step, not the builder's, so it is deliberately absent here.
//
// FOCUSED VERIFICATION (at head 5ef25354b9e0609f6d42bd95edcec7c6db1b29c2, the
// lane tip these commands describe; this comment is the only later edit)
//   go vet ./internal/remoteaccess/ ./internal/app/ ./internal/server/ ./internal/nodehost/
//   go test -race -count=1 ./internal/remoteaccess/
//   go test -count=1 ./internal/server/ ./internal/nodehost/ ./internal/conformance/ ./internal/app/
//   UTC 2026-08-20T11:40:08Z -> 2026-08-20T11:41:16Z, elapsed 68s wall
//   (go test runs the packages in parallel), exit 0
//   remoteaccess (race) 1.512s · server 61.167s · nodehost 0.820s ·
//   conformance 17.614s · app 63.582s  (all ok)
//   log: builder scratchpad AUDITFIX-TLS-MUX-final-verify.log; the same receipt
//   text lives untracked in .agent-evidence/ for humans.
//
// DELIBERATE-BREAK RECORD (each invariant broken once, observed to fail, then
// restored; the remoteaccess suite re-ran green after every restore):
//   BREAK 1 — Close left pending (mid-classification) connections unclosed:
//     --- FAIL: TestTLSMuxCloseInterruptsStalledClassification (1.01s)
//         tlsmux_test.go:81: stalled client still connected after Close (read:
//         read tcp 127.0.0.1:53069->127.0.0.1:53068: i/o timeout): connection stranded
//     --- FAIL: TestTLSMuxReloadFencesPredecessor (2.01s)
//         tlsmux_test.go:272: mux goroutines still running after Close: goroutine leak
//   BREAK 2 — classification made serial again (the old in-Accept peek shape):
//     --- FAIL: TestTLSMuxStalledClientDoesNotDelayOthers (2.01s)
//         tlsmux_test.go:122: request behind a stalled client did not complete:
//         Get "http://127.0.0.1:53105/": context deadline exceeded
//         (Client.Timeout exceeded while awaiting headers)
//   BREAK 3 — Close left queued (classified, unclaimed) connections unclosed:
//     --- FAIL: TestTLSMuxCloseRetiresQueuedConnections (1.01s)
//         tlsmux_test.go:231: queued connection still open after Close (read:
//         read tcp 127.0.0.1:53283->127.0.0.1:53282: i/o timeout): connection stranded
//   BREAK 4 — Accept returned a non-net.ErrClosed error after Close:
//     --- FAIL: TestTLSMuxAcceptCloseRace (0.05s)
//         tlsmux_test.go:173: Accept ended with BREAK 5: wrong close error, want net.ErrClosed
//         tlsmux_test.go:198: Accept after Close = BREAK 5: wrong close error, want net.ErrClosed
//     (the injected error string was labeled "BREAK 5" in that run; it is the
//     same break recorded here as BREAK 4)
//   PROBE (no failure, by design) — omitting Close's cond.Broadcast alone does
//     not fail TestTLSMuxAcceptCloseRace: the accept pump broadcasts again when
//     the closed inner listener errors, so Close's broadcast is redundancy, not
//     a lone wake path.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Evidence — SECAUDIT-20260820-TLSMUX-ACCEPT-RESILIENCE-1 (2026-08-20)
//
// Builder-lane verification at head
// a90ed09be6a475e2c2c6903336245ca367191f84; the only later edit is this
// evidence receipt. The full make gate is owned by the orchestrator and was
// not run by this builder.
//
// FOCUSED VERIFICATION
//   command: go test ./internal/remoteaccess/... -race -count=1
//   UTC 2026-08-20T17:09:02Z -> 2026-08-20T17:09:04Z, elapsed 2s, exit 0
//   output: ok github.com/beamfall/core/internal/remoteaccess 1.541s
//   log: .agent-evidence/SECAUDIT-20260820-TLSMUX-ACCEPT-RESILIENCE-1-final-verify.log
//
// DELIBERATE-BREAK RECORD (each new regression observed failing, then restored)
//   command: go test ./internal/remoteaccess/... -race -run
//     'TestTLSMux(RetriesTemporaryAcceptError|BoundsInFlightClassifications)$'
//     -count=1
//   temporary-error retirement:
//     --- FAIL: TestTLSMuxRetriesTemporaryAcceptError (0.00s)
//         tlsmux_test.go:121: Accept after temporary error = temporary accept
//         failure, want connection
//   unbounded classification:
//     --- FAIL: TestTLSMuxBoundsInFlightClassifications (0.01s)
//         tlsmux_test.go:177: in-flight classifications = 72, want cap 64
//   command: go test ./internal/remoteaccess/... -race -run
//     '^TestTLSMuxPermanentAcceptErrorTerminates$' -count=1
//   permanent-error substitution:
//     --- FAIL: TestTLSMuxPermanentAcceptErrorTerminates (0.00s)
//         tlsmux_test.go:136: Accept error = use of closed network connection,
//         want permanent accept failure
// ---------------------------------------------------------------------------
