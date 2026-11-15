package encderiv

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// tokenBytes is the bearer token's entropy: 256 bits.
	tokenBytes = 32
	// sourcePath is the only resource the endpoint serves under its token.
	sourcePath = "source"
	// copyChunkBytes bounds each body write so revocation and expiry are
	// observed between chunks.
	copyChunkBytes = 64 << 10
	// readHeaderTimeout bounds a request that never finishes its headers.
	readHeaderTimeout = 10 * time.Second
	// maxHeaderBytes bounds the request headers the endpoint will parse.
	maxHeaderBytes = 16 << 10
)

// Opener returns a fresh plaintext view of the source. The endpoint opens one
// per request and closes it when the response ends.
type Opener func() (io.ReadSeekCloser, error)

// peerChecker refuses a connection whose peer socket is not owned by this
// process's user. On Linux it reads the kernel socket tables; elsewhere it
// accepts (macOS peer verification is a deferred ADR-0235 item).
type peerChecker func(peer, server netip.AddrPort) error

var (
	errForbidden     = errors.New("encderiv: request refused")
	errSourceFailure = errors.New("encderiv: source could not be read")
)

// endpoint serves one source over loopback HTTP for the life of one job.
type endpoint struct {
	token    string
	host     string
	server   netip.AddrPort
	size     int64
	idle     time.Duration
	deadline time.Time
	now      func() time.Time
	peer     peerChecker

	srv       *http.Server
	serveDone chan struct{}

	mu       sync.Mutex
	open     Opener
	revoked  bool
	expired  bool
	active   int
	lastSeen time.Time
	failErr  error
	inflight sync.WaitGroup

	revokeOnce sync.Once
}

type endpointConfig struct {
	open     Opener
	size     int64
	deadline time.Time
	idle     time.Duration
	now      func() time.Time
	peer     peerChecker
}

// startEndpoint listens on an ephemeral IPv4 loopback port and serves until
// Revoke. The listener never binds a non-loopback address.
func startEndpoint(cfg endpointConfig) (*endpoint, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("encderiv: token: %w", err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("encderiv: listen: %w", err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, errors.New("encderiv: listener has no TCP address")
	}
	server := tcp.AddrPort()
	server = netip.AddrPortFrom(server.Addr().Unmap(), server.Port())
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	e := &endpoint{
		token:     hex.EncodeToString(raw),
		host:      server.String(),
		server:    server,
		size:      cfg.size,
		idle:      cfg.idle,
		deadline:  cfg.deadline,
		now:       now,
		peer:      cfg.peer,
		open:      cfg.open,
		lastSeen:  now(),
		serveDone: make(chan struct{}),
	}
	clear(raw)
	e.srv = &http.Server{
		Handler:           e,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       cfg.idle,
		MaxHeaderBytes:    maxHeaderBytes,
		// The request line carries the token; the server's own diagnostics
		// never reach a log.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	go func() {
		defer close(e.serveDone)
		_ = e.srv.Serve(ln)
	}()
	return e, nil
}

// URL is the transcoder's input. It carries the token, so it is a secret.
func (e *endpoint) URL() string {
	return "http://" + e.host + "/" + e.token + "/" + sourcePath
}

// Port is the loopback port the transcoder's sandbox must be allowed to reach.
func (e *endpoint) Port() int { return int(e.server.Port()) }

// Err reports the first failure that should fail the job even if the
// transcoder exits cleanly: a source that could not be read, or a token used
// after it was revoked or expired.
func (e *endpoint) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.failErr
}

// Revoke ends the token: new requests are refused, open connections are closed
// so in-flight bodies abort, and it returns only once every handler has
// returned and the server has stopped. It is safe to call more than once and
// from more than one goroutine.
func (e *endpoint) Revoke() {
	e.revokeOnce.Do(func() {
		e.mu.Lock()
		e.revoked = true
		e.mu.Unlock()
		_ = e.srv.Close()
		e.inflight.Wait()
		<-e.serveDone
		e.mu.Lock()
		e.open = nil
		e.mu.Unlock()
	})
}

func (e *endpoint) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failErr == nil {
		e.failErr = err
	}
}

// enter admits one request while the token is live and registers it so
// Revoke waits for it.
func (e *endpoint) enter() (Opener, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.revoked || e.open == nil {
		return nil, ErrRevoked
	}
	now := e.now()
	if e.expired || !now.Before(e.deadline) || (e.active == 0 && now.Sub(e.lastSeen) > e.idle) {
		e.expired = true
		return nil, ErrExpired
	}
	e.active++
	e.lastSeen = now
	e.inflight.Add(1)
	return e.open, nil
}

func (e *endpoint) exit() {
	e.mu.Lock()
	e.active--
	e.lastSeen = e.now()
	e.mu.Unlock()
	e.inflight.Done()
}

// live reports whether an admitted response may write its next chunk.
func (e *endpoint) live() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.revoked {
		return ErrRevoked
	}
	now := e.now()
	if e.expired || !now.Before(e.deadline) {
		e.expired = true
		return ErrExpired
	}
	e.lastSeen = now
	return nil
}

func (e *endpoint) tokenMatches(path string) bool {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok {
		return false
	}
	token, resource, ok := strings.Cut(rest, "/")
	if !ok || resource != sourcePath {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(e.token)) == 1
}

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().Unmap().IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !e.tokenMatches(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	// From here the caller has proven the token, so every refusal also fails
	// the job: a token presented wrongly is a token that may have leaked.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		e.fail(errForbidden)
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Host != e.host {
		e.fail(errForbidden)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if e.peer != nil {
		if err := e.peer(netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port()), e.server); err != nil {
			e.fail(errForbidden)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	open, err := e.enter()
	if err != nil {
		e.fail(err)
		http.Error(w, "gone", http.StatusGone)
		return
	}
	defer e.exit()
	e.serveRange(w, r, open)
}

func (e *endpoint) serveRange(w http.ResponseWriter, r *http.Request, open Opener) {
	start, end, partial, ok := parseRange(r.Header.Get("Range"), e.size)
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Accept-Ranges", "bytes")
	if !ok {
		h.Set("Content-Range", "bytes */"+strconv.FormatInt(e.size, 10))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	src, err := open()
	if err != nil {
		e.fail(errSourceFailure)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = src.Close() }()
	if _, err := src.Seek(start, io.SeekStart); err != nil {
		e.fail(errSourceFailure)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	length := end - start + 1
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, e.size))
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	buf := make([]byte, copyChunkBytes)
	defer clear(buf)
	for length > 0 {
		n := int(min(length, int64(len(buf))))
		if _, err := io.ReadFull(src, buf[:n]); err != nil {
			// A source that fails authentication or ends before its declared
			// length must fail the job even if the transcoder exits cleanly on
			// the short input it saw.
			e.fail(errSourceFailure)
			panic(http.ErrAbortHandler)
		}
		// Checked after the read, which can block, so no chunk is written once
		// the token has ended.
		if err := e.live(); err != nil {
			e.fail(err)
			panic(http.ErrAbortHandler)
		}
		if _, err := w.Write(buf[:n]); err != nil {
			// The transcoder closed the connection (a seek, or it is done), or
			// revocation closed it under the write.
			if err := e.live(); err != nil {
				e.fail(err)
			}
			return
		}
		length -= int64(n)
	}
}

// parseRange accepts no Range header or one single bytes=N-M / bytes=N- range
// and reports whether it is satisfiable. Suffix ranges, multiple ranges, other
// units and ranges starting at or beyond the end are refused (416).
func parseRange(header string, size int64) (start, end int64, partial, ok bool) {
	if header == "" {
		return 0, size - 1, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	start, okStart := parseDigits(first)
	if !okStart || start >= size {
		return 0, 0, false, false
	}
	end = size - 1
	if last != "" {
		parsed, okEnd := parseDigits(last)
		if !okEnd || parsed < start {
			return 0, 0, false, false
		}
		end = min(parsed, size-1)
	}
	return start, end, true, true
}

func parseDigits(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}
