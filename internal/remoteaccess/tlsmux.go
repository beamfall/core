package remoteaccess

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"
)

// NewTLSMuxListener wraps a raw TCP listener so a SINGLE bound port serves both
// cleartext HTTP (the LAN path — ADR-0012 §1 permits cleartext to private/
// link-local destinations) and TLS (the overlay path and any direct client that
// speaks https). This is what un-breaks the overlay dead-endpoint:
// ResolveEndpoints advertises the overlay candidate as https://<overlay-ip>:
// <lanPort>, and the LAN candidate as http://<lan-ip>:<lanPort> — SAME port. A
// plain http-only or tls-only listener could satisfy only one; peeking the first
// byte routes each connection to the right handler so BOTH advertised schemes
// actually handshake.
//
// The classification is the TLS record content-type of the first byte: a TLS
// ClientHello opens with a Handshake record (0x16). Anything else (an HTTP
// method's first ASCII letter) is served as cleartext. The peeked byte is
// buffered and replayed, so neither the TLS stack nor the HTTP parser loses it.
//
// Ownership contract (AUDITFIX-TLS-MUX): the mux owns every connection it has
// accepted from the inner listener until Accept hands it to the caller. Close
// fences atomically — after Close begins, Accept never returns another
// connection, and every connection still owned by the mux (mid-classification
// or classified-but-unclaimed) is closed before Close returns. Nothing is
// stranded across a shutdown or a listener reload: a restarted listener can
// never race a predecessor's leftover connections onto a stale TLS config.
func NewTLSMuxListener(inner net.Listener, conf *tls.Config) net.Listener {
	l := &tlsMuxListener{
		inner:         inner,
		conf:          conf,
		classifySlots: make(chan struct{}, tlsMuxMaxInFlightClassifications),
		pending:       make(map[net.Conn]struct{}),
		done:          make(chan struct{}),
	}
	l.cond = sync.NewCond(&l.mu)
	l.wg.Add(1)
	go l.acceptLoop()
	return l
}

// tlsRecordHandshake is the TLS record content-type byte for a Handshake record
// (RFC 8446 §5.1) — the first byte of every ClientHello.
const tlsRecordHandshake = 0x16

// tlsMuxPeekTimeout bounds the classification read on a freshly-accepted
// connection. Without it, Peek(1) blocks until the client sends its first byte,
// so a single client that connects and then stays silent would pin its
// classification goroutine forever — an unauthenticated pre-TLS slowloris.
// A legitimate LAN/overlay client sends its first byte (HTTP verb
// or TLS ClientHello record) immediately, well within this window.
const tlsMuxPeekTimeout = 5 * time.Second

// tlsMuxMaxInFlightClassifications caps unauthenticated connections waiting to
// reveal their first byte. Once full, the accept pump closes newly-accepted
// connections immediately instead of queueing them in userspace.
const tlsMuxMaxInFlightClassifications = 64

// These delays mirror net/http.Server's temporary Accept-error backoff.
const (
	tlsMuxInitialAcceptRetryDelay = 5 * time.Millisecond
	tlsMuxMaxAcceptRetryDelay     = time.Second
)

// tlsMuxListener classifies each accepted connection concurrently: an accept
// pump pulls raw connections off the inner listener and spawns classification
// goroutines up to tlsMuxMaxInFlightClassifications, so a stalled client burns
// only its own bounded slot for at most tlsMuxPeekTimeout and never delays an
// already-admitted handshake (the old in-Accept peek serialized every
// connection behind the slowest). Once the cap is full, newly-accepted
// connections are refused and closed immediately; they are never queued in
// userspace where a silent-connect flood could grow without bound.
//
// All mutable state is fenced by mu: pending (accepted, classification in
// flight), queue (classified, awaiting Accept), closed (the shutdown fence),
// and acceptErr (the inner listener's terminal error). Every connection is in
// exactly one place at a time — pending, queue, or the caller's hands — so
// Close can enumerate and deterministically close everything it still owns.
type tlsMuxListener struct {
	inner net.Listener
	conf  *tls.Config

	classifySlots chan struct{}

	mu        sync.Mutex
	cond      *sync.Cond
	pending   map[net.Conn]struct{}
	queue     []net.Conn
	closed    bool
	acceptErr error
	done      chan struct{}

	// wg counts the accept pump and every live classification goroutine, so a
	// test (or any owner) can prove the mux left nothing running after Close.
	wg sync.WaitGroup
}

// acceptLoop is the single accept pump: it owns the inner Accept call and
// spawns classification goroutines up to the admission cap. Temporary inner
// errors retry with bounded net/http-style backoff; only permanent errors are
// terminal and surfaced by Accept after the queue drains. Close interrupts
// either a blocked inner Accept or a retry delay.
func (l *tlsMuxListener) acceptLoop() {
	defer l.wg.Done()
	var retryDelay time.Duration
	for {
		raw, err := l.inner.Accept()
		if err != nil {
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if closed {
				return
			}
			if isTransientAcceptError(err) {
				if retryDelay == 0 {
					retryDelay = tlsMuxInitialAcceptRetryDelay
				} else {
					retryDelay *= 2
				}
				if retryDelay > tlsMuxMaxAcceptRetryDelay {
					retryDelay = tlsMuxMaxAcceptRetryDelay
				}
				timer := time.NewTimer(retryDelay)
				select {
				case <-timer.C:
					continue
				case <-l.done:
					timer.Stop()
					return
				}
			}
			l.mu.Lock()
			if !l.closed && l.acceptErr == nil {
				l.acceptErr = err
			}
			l.cond.Broadcast()
			l.mu.Unlock()
			return
		}
		retryDelay = 0
		select {
		case l.classifySlots <- struct{}{}:
			// Admitted below. The classifier releases the slot on exit.
		default:
			// Refuse overload instead of replacing kernel-backlog pressure
			// with an unbounded userspace classification queue.
			_ = raw.Close()
			continue
		}
		if !l.admit(raw) {
			// Shutdown has begun: reject new work rather than classify it.
			<-l.classifySlots
			_ = raw.Close()
			continue
		}
		l.wg.Add(1)
		go l.classify(raw)
	}
}

// isTransientAcceptError matches the retryable accept failures recognized by
// net/http.Server.Serve, plus concrete errno fallbacks for wrapped errors.
func isTransientAcceptError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		// net.Error.Temporary is deprecated, but net/http.Server.Serve still
		// uses it for accept retries; matching that behavior is deliberate.
		if netErr.Temporary() { //nolint:staticcheck // Deliberately matches net/http.Server.Serve.
			return true
		}
	}
	for _, transient := range []error{
		syscall.EINTR,
		syscall.EMFILE,
		syscall.ENFILE,
		syscall.ECONNABORTED,
		syscall.ECONNRESET,
	} {
		if errors.Is(err, transient) {
			return true
		}
	}
	return false
}

// admit registers a freshly-accepted connection as pending, refusing it when
// the mux is already closed — the accept-side half of the shutdown fence.
func (l *tlsMuxListener) admit(raw net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.pending[raw] = struct{}{}
	return true
}

// classify peeks the first byte of one connection and routes it: a TLS
// handshake record is wrapped by tls.Server (the config's certificate callbacks
// keep rotation and SNI composition live), anything else is served as cleartext
// with the peeked byte replayed. The peek is deadline-bounded, and a concurrent
// Close closes the raw connection, which unblocks the peek immediately — so a
// stalled client can neither wedge classification nor outlive shutdown.
func (l *tlsMuxListener) classify(raw net.Conn) {
	defer l.wg.Done()
	defer func() { <-l.classifySlots }()
	peeked := newPeekConn(raw)
	// Bound the peek: cleared immediately after classification so the short
	// deadline never applies to the long-lived connection handed downstream.
	_ = raw.SetReadDeadline(time.Now().Add(tlsMuxPeekTimeout))
	first, perr := peeked.peekFirstByte()
	_ = raw.SetReadDeadline(time.Time{})
	if perr != nil {
		// Silent/slow or hung-up client — or a connection closed under us by
		// Close. Either way the connection is dropped, never surfaced.
		l.abort(raw)
		return
	}
	var conn net.Conn = peeked
	if first == tlsRecordHandshake {
		conn = tls.Server(peeked, l.conf)
	}
	if !l.deliver(raw, conn) {
		// Close won the race after classification finished: the connection is
		// the mux's to retire, deterministically, right now.
		_ = conn.Close()
	}
}

// abort retires a connection whose classification failed: it leaves pending and
// is closed. Safe against a concurrent Close having already closed the raw
// connection (the double close is a no-op error, ignored).
func (l *tlsMuxListener) abort(raw net.Conn) {
	l.mu.Lock()
	delete(l.pending, raw)
	l.mu.Unlock()
	_ = raw.Close()
}

// deliver atomically moves a classified connection from pending to the accept
// queue. It refuses (returning false) when the mux closed in the meantime, so a
// connection can never slip into the queue after Close has drained it.
func (l *tlsMuxListener) deliver(raw, conn net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pending, raw)
	if l.closed {
		return false
	}
	l.queue = append(l.queue, conn)
	l.cond.Signal()
	return true
}

// Accept returns the next classified connection. After Close it always returns
// net.ErrClosed — never a leftover connection — and once the inner listener has
// failed it surfaces that error after the already-classified queue drains.
func (l *tlsMuxListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		if l.closed {
			return nil, net.ErrClosed
		}
		if len(l.queue) > 0 {
			conn := l.queue[0]
			l.queue = l.queue[1:]
			return conn, nil
		}
		if l.acceptErr != nil {
			return nil, l.acceptErr
		}
		l.cond.Wait()
	}
}

// Close fences the mux atomically: it marks the listener closed (so Accept,
// admit, and deliver all refuse from this point), closes the inner listener
// (stopping the accept pump), and closes every connection the mux still owns —
// both mid-classification (unblocking their peeks immediately) and classified-
// but-unclaimed. When Close returns, every connection the mux ever accepted has
// either been handed to the caller via Accept or closed. Subsequent Closes
// return net.ErrClosed, mirroring net.TCPListener.
func (l *tlsMuxListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return net.ErrClosed
	}
	l.closed = true
	close(l.done)
	pending := make([]net.Conn, 0, len(l.pending))
	for c := range l.pending {
		pending = append(pending, c)
	}
	l.pending = nil
	queued := l.queue
	l.queue = nil
	l.cond.Broadcast()
	l.mu.Unlock()

	err := l.inner.Close()
	for _, c := range pending {
		_ = c.Close()
	}
	for _, c := range queued {
		_ = c.Close()
	}
	return err
}

// Addr reports the inner listener's address.
func (l *tlsMuxListener) Addr() net.Addr {
	return l.inner.Addr()
}

// peekConn buffers the first byte read from the wire so classification does not
// consume it: the byte is replayed to whichever handler (TLS or HTTP) reads next.
type peekConn struct {
	net.Conn
	reader *bufio.Reader
}

func newPeekConn(c net.Conn) *peekConn {
	return &peekConn{Conn: c, reader: bufio.NewReader(c)}
}

// peekFirstByte reads-ahead one byte without consuming it from the buffered
// reader, so the subsequent Read (by the TLS or HTTP server) still sees it.
func (c *peekConn) peekFirstByte() (byte, error) {
	b, err := c.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// Read serves bytes from the buffer first (including the peeked byte), then from
// the wire, so the replay is transparent to the caller.
func (c *peekConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
