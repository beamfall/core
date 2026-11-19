package encderiv

import (
	"context"
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
	// outPath is the prefix of the upload resources a live run accepts
	// (ADR-0236 D1): out/<rung>/<name>.
	outPath = "out"
)

// Opener returns a fresh plaintext view of the source. The endpoint opens one
// per request and closes it when the response ends.
type Opener func() (io.ReadSeekCloser, error)

// peerChecker refuses a connection whose peer socket is not owned by this
// process's user. On Linux it reads the kernel socket tables; elsewhere it
// accepts (macOS peer verification is a deferred ADR-0235 item).
type peerChecker func(peer, server netip.AddrPort) error

// uploadSink stores the uploads a live run's encoder sends (ADR-0236 D1). The
// endpoint routes only out/<rung>/<name> for its own rung to it.
type uploadSink interface {
	// uploadLimit reports whether name is on the closed upload list and the
	// most bytes its body may carry.
	uploadLimit(name string) (limit int64, ok bool)
	// upload consumes body and stores it under name. errUploadExists means the
	// name is already published and nothing was read.
	upload(ctx context.Context, name string, body io.Reader, check func() error) error
}

var (
	errForbidden      = errors.New("encderiv: request refused")
	errSourceFailure  = errors.New("encderiv: source could not be read")
	errUploadFailure  = errors.New("encderiv: upload could not be stored")
	errUploadTooLarge = errors.New("encderiv: upload exceeds its size limit")
	// errUploadTruncated is a body that ended before its declared end. It
	// must not reach the encryptor as io.ErrUnexpectedEOF, which there means
	// a short final frame, or the partial file would be published.
	errUploadTruncated = errors.New("encderiv: upload body ended early")
	errUploadExists    = errors.New("encderiv: upload name already published")
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

	// sink and rung, when sink is set, accept the encoder's uploads under
	// out/<rung>/ (a live run, ADR-0236 D1).
	sink uploadSink
	rung string

	srv           *http.Server
	serveDone     chan struct{}
	uploadCtx     context.Context
	cancelUploads context.CancelFunc
	// failed is closed by the first failure, so a live run's owner can kill the
	// encoder at once instead of waiting for it to exit.
	failed chan struct{}

	mu            sync.Mutex
	open          Opener
	revoked       bool
	expired       bool
	active        int
	lastSeen      time.Time
	idleSuspended bool
	uploadReads   map[*http.ResponseController]struct{}
	failErr       error
	inflight      sync.WaitGroup

	revokeOnce sync.Once
}

type endpointConfig struct {
	open     Opener
	size     int64
	deadline time.Time
	idle     time.Duration
	now      func() time.Time
	peer     peerChecker
	sink     uploadSink
	rung     string
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
	uploadCtx, cancelUploads := context.WithCancel(context.Background())
	e := &endpoint{
		uploadCtx:     uploadCtx,
		cancelUploads: cancelUploads,
		token:         hex.EncodeToString(raw),
		host:          server.String(),
		server:        server,
		size:          cfg.size,
		idle:          cfg.idle,
		deadline:      cfg.deadline,
		now:           now,
		peer:          cfg.peer,
		open:          cfg.open,
		sink:          cfg.sink,
		rung:          cfg.rung,
		lastSeen:      now(),
		serveDone:     make(chan struct{}),
		failed:        make(chan struct{}),
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

// OutputURL is the base a live run's encoder uploads under: out/<rung>. It
// carries the token, so it is a secret.
func (e *endpoint) OutputURL() string {
	return "http://" + e.host + "/" + e.token + "/" + outPath + "/" + e.rung
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
		e.cancelUploads()
		_ = e.srv.Close()
		e.inflight.Wait()
		<-e.serveDone
		e.mu.Lock()
		e.open = nil
		e.mu.Unlock()
	})
}

// Drain revokes the token once the requests already sent have been served.
// It stops accepting connections and lets the handlers in flight finish
// before revoking. Use it after the encoder has exited: FFmpeg ends an upload
// without waiting for the answer, so its last upload can still be arriving
// when it exits, and Revoke would cut it off. With the client gone, each
// handler ends at its body's end, or within the idle timeout at worst.
func (e *endpoint) Drain() {
	wait := e.idle
	if wait <= 0 {
		wait = readHeaderTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_ = e.srv.Shutdown(ctx)
	e.Revoke()
}

// Failed is closed once the endpoint has recorded a failure (see Err).
func (e *endpoint) Failed() <-chan struct{} { return e.failed }

func (e *endpoint) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failErr == nil {
		e.failErr = err
		close(e.failed)
	}
}

// expire ends the token because the encoder stopped making progress.
func (e *endpoint) expire() {
	e.mu.Lock()
	e.expired = true
	e.mu.Unlock()
	e.fail(ErrExpired)
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
	if e.expired || !now.Before(e.deadline) || (!e.idleSuspended && e.active == 0 && now.Sub(e.lastSeen) > e.idle) {
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

// route returns the resource named after the token, and false when the path
// does not carry the token.
func (e *endpoint) route(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/")
	if !ok {
		return "", false
	}
	token, resource, ok := strings.Cut(rest, "/")
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(e.token)) != 1 {
		return "", false
	}
	return resource, true
}

// uploadTarget accepts out/<rung>/<name> for this run's rung and a name on the
// sink's closed list.
func (e *endpoint) uploadTarget(resource string) (string, int64, bool) {
	name, ok := strings.CutPrefix(resource, outPath+"/"+e.rung+"/")
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", 0, false
	}
	limit, ok := e.sink.uploadLimit(name)
	return name, limit, ok
}

func (e *endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().Unmap().IsLoopback() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	resource, ok := e.route(r.URL.Path)
	if !ok || (resource != sourcePath && e.sink == nil) {
		http.NotFound(w, r)
		return
	}
	// From here the caller has proven the token, so every refusal also fails
	// the job: a token presented wrongly is a token that may have leaked.
	var upload string
	var limit int64
	if resource == sourcePath {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			e.fail(errForbidden)
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	} else {
		if upload, limit, ok = e.uploadTarget(resource); !ok {
			e.fail(errForbidden)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPut {
			e.fail(errForbidden)
			w.Header().Set("Allow", http.MethodPut)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
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
	if upload != "" {
		e.receive(w, r, upload, limit)
		return
	}
	e.serveRange(w, r, open)
}

// receive hands one upload body to the sink. The body counts as encoder
// activity while it arrives, and a body that stalls for the idle timeout ends
// the token (ADR-0236 D3).
func (e *endpoint) receive(w http.ResponseWriter, r *http.Request, name string, limit int64) {
	w.Header().Set("Cache-Control", "no-store")
	if r.ContentLength > limit {
		w.Header().Set("Connection", "close")
		e.fail(errUploadTooLarge)
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	rc := http.NewResponseController(w)
	defer e.endUploadRead(rc)
	body := &uploadBody{e: e, src: r.Body, rc: rc, remaining: limit}
	// FFmpeg can close its connection after sending the complete body,
	// before encryption finishes. Only revoking the endpoint cancels a
	// completed upload that is still waiting to publish.
	err := e.sink.upload(e.uploadCtx, name, body, e.live)
	switch {
	case err == nil && body.err == nil:
		w.WriteHeader(http.StatusCreated)
	case errors.Is(err, errUploadExists) && body.err == nil:
		// A restarted encoder sends names an earlier run of the same session
		// already published (init.mp4 every time). The first copy stays; the
		// body is drained so the encoder's write completes, and the run goes on.
		if _, err := io.Copy(io.Discard, body); err != nil {
			e.failUpload(w, body.err)
			return
		}
		http.Error(w, "exists", http.StatusConflict)
	default:
		if body.err != nil {
			err = body.err
		}
		e.failUpload(w, err)
	}
}

// failUpload records an upload that did not complete and answers it. cause is
// the body's own failure, nil when the sink failed. The connection closes
// after the answer: otherwise the server drains the rest of the body first,
// with no deadline, and a stalled encoder never sees the refusal.
func (e *endpoint) failUpload(w http.ResponseWriter, cause error) {
	w.Header().Set("Connection", "close")
	switch {
	case errors.Is(cause, errUploadTooLarge):
		e.fail(errUploadTooLarge)
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
	case errors.Is(cause, ErrRevoked), errors.Is(cause, ErrExpired):
		e.fail(cause)
		http.Error(w, "gone", http.StatusGone)
	case isTimeout(cause):
		e.expire()
		http.Error(w, "gone", http.StatusGone)
	default:
		e.fail(errUploadFailure)
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// setIdleSuspended suspends idle expiry, including read deadlines already
// armed on in-flight uploads. The token's absolute lifetime still applies.
func (e *endpoint) setIdleSuspended(suspended bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.idleSuspended = suspended
	e.lastSeen = e.now()
	for rc := range e.uploadReads {
		_ = rc.SetReadDeadline(e.uploadReadDeadlineLocked())
	}
}

func (e *endpoint) uploadReadDeadlineLocked() time.Time {
	if e.idleSuspended {
		return time.Time{}
	}
	return time.Now().Add(e.idle)
}

func (e *endpoint) beginUploadRead(rc *http.ResponseController) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.idle <= 0 {
		return nil
	}
	if e.uploadReads == nil {
		e.uploadReads = make(map[*http.ResponseController]struct{})
	}
	e.uploadReads[rc] = struct{}{}
	return rc.SetReadDeadline(e.uploadReadDeadlineLocked())
}

func (e *endpoint) endUploadRead(rc *http.ResponseController) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.uploadReads, rc)
	_ = rc.SetReadDeadline(time.Time{})
}

// uploadBody is one upload's request body as the sink reads it: each read is
// admitted only while the token is live, may block at most the idle timeout,
// and may not take the body past its limit. err keeps the first failure so
// the endpoint can tell a body failure from a sink failure.
type uploadBody struct {
	e         *endpoint
	src       io.Reader
	rc        *http.ResponseController
	remaining int64
	err       error
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if err := b.e.live(); err != nil {
		b.err = err
		return 0, err
	}
	if err := b.e.beginUploadRead(b.rc); err != nil {
		b.err = err
		return 0, err
	}
	// One byte past the limit is enough to tell an oversized body.
	if int64(len(p)) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.src.Read(p)
	if int64(n) > b.remaining {
		b.err = errUploadTooLarge
		return 0, b.err
	}
	b.remaining -= int64(n)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = errUploadTruncated
	}
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
		return n, err
	}
	// Checked again after the read, which can block, so a body that finishes
	// after revocation is never published.
	if liveErr := b.e.live(); liveErr != nil {
		b.err = liveErr
		return 0, liveErr
	}
	return n, err
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
