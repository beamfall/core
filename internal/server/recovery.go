package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"runtime/debug"
	"strings"
	"time"
)

// recoverPanics keeps a broken handler from taking down its serving goroutine
// while preserving the request-ID correlation contract for the resulting 5xx.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalHeader := w.Header().Clone()
		tracked, wrapped := trackResponseWriter(w)
		defer func() {
			panicValue := recover()
			if panicValue == nil {
				return
			}
			if panicValue == http.ErrAbortHandler {
				panic(http.ErrAbortHandler)
			}

			attrs := []any{
				"status", http.StatusInternalServerError,
				"method", r.Method,
				"path", redactTokenPath(r.URL),
				"panic", fmt.Sprint(panicValue),
				"stack", string(debug.Stack()),
			}
			// The mux sets r.Pattern in place before dispatch, so the matched
			// route survives redaction as its own attribute (empty when the
			// panic fired before routing).
			if r.Pattern != "" {
				attrs = append(attrs, "route", r.Pattern)
			}
			if reqID := requestIDFromContext(r.Context()); reqID != "" {
				attrs = append(attrs, "requestID", reqID)
			}
			slog.ErrorContext(r.Context(), "http request panic", attrs...)

			if !tracked.committed {
				restoreHeader(wrapped.Header(), originalHeader)
				writeJSON(wrapped, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			}
		}()

		next.ServeHTTP(wrapped, r)
	})
}

// tokenRoutePrefixes lists the route prefixes whose first path segment after
// the prefix is a URL-carried Stream Token: the media byte routes, the
// proxy-tee stream, and the Offline Pack artifact routes. A log line must never
// carry that segment verbatim — a captured log would replay as a credential.
var tokenRoutePrefixes = []string{
	"/api/media/direct/",
	"/api/media/hls/",
	"/api/media/assets/",
	"/api/media/subtitle/",
	"/api/media/sidecar/",
	"/api/media/artwork-candidate/",
	"/api/media/proxy/",
	"/api/pack/manifest/",
	"/api/pack/content/",
	"/api/pack/key/",
	"/api/pack/status/",
}

const redactedTokenSegment = "[redacted]"

// redactTokenPath is the loggable form of u's path: normalized, with every
// token segment replaced. It fails closed: repeated slashes and dot segments
// are resolved first (the logger runs even when routing redirects or rejects
// the raw path), a token prefix is matched anywhere in the path, and when the
// raw path carries an encoded slash nothing after the prefix is kept, because
// the decoded segment boundary no longer marks where the token ends. The query
// string is never part of the result.
func redactTokenPath(u *url.URL) string {
	clean := normalizeLogPath(u.Path)
	if strings.Contains(strings.ToLower(u.RawPath), "%2f") {
		if i, prefix := firstTokenPrefix(clean); i >= 0 {
			return clean[:i+len(prefix)] + redactedTokenSegment
		}
		return clean
	}
	return redactTokenSegments(clean)
}

// redactTokenSegments replaces the segment after each token prefix in a
// normalized path, keeping the non-secret segments as diagnostics.
func redactTokenSegments(clean string) string {
	i, prefix := firstTokenPrefix(clean)
	if i < 0 {
		return clean
	}
	head := clean[:i+len(prefix)] + redactedTokenSegment
	_, suffix, hasSuffix := strings.Cut(clean[i+len(prefix):], "/")
	if !hasSuffix {
		return head
	}
	return head + redactTokenSegments("/"+suffix)
}

// firstTokenPrefix reports the earliest token prefix in a normalized path and
// its index, or -1.
func firstTokenPrefix(clean string) (int, string) {
	first, match := -1, ""
	for _, prefix := range tokenRoutePrefixes {
		if i := strings.Index(clean, prefix); i >= 0 && (first < 0 || i < first) {
			first, match = i, prefix
		}
	}
	return first, match
}

// isTokenRoutePath reports whether a request path carries a Stream Token.
func isTokenRoutePath(u *url.URL) bool {
	i, _ := firstTokenPrefix(normalizeLogPath(u.Path))
	return i >= 0
}

// normalizeLogPath collapses repeated slashes and resolves dot segments,
// keeping a trailing slash so a bare token prefix still matches.
func normalizeLogPath(p string) string {
	clean := path.Clean("/" + p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean
}

func restoreHeader(dst, src http.Header) {
	clear(dst)
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
}

type trackedResponseWriter struct {
	http.ResponseWriter
	committed bool
	// status is the final status sent, set once the response commits.
	status int
	// committedAt is when the response started (its header was committed).
	committedAt time.Time
}

func (w *trackedResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	// Informational responses do not commit the final response, except for the
	// protocol-switching 101 response.
	if status == http.StatusSwitchingProtocols || status >= http.StatusOK {
		w.commit(status)
	}
}

func (w *trackedResponseWriter) Write(p []byte) (int, error) {
	w.commit(http.StatusOK)
	return w.ResponseWriter.Write(p)
}

// ReadFrom keeps the underlying writer's io.ReaderFrom reachable through the
// wrapper, so http.ServeContent's copy still takes the sendfile path for
// direct play instead of a userspace buffer loop.
func (w *trackedResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	w.commit(http.StatusOK)
	return readFrom(w.ResponseWriter, src)
}

// readFrom copies src into w through w's own io.ReaderFrom when it has one.
func readFrom(w http.ResponseWriter, src io.Reader) (int64, error) {
	if rf, ok := w.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(w, src)
}

func (w *trackedResponseWriter) commit(status int) {
	if !w.committed {
		w.status = status
		w.committedAt = time.Now()
	}
	w.committed = true
}

// firstByte reports how long after started the response began, or elapsed for
// a handler that returned without writing (net/http commits it then).
func (w *trackedResponseWriter) firstByte(started time.Time, elapsed time.Duration) time.Duration {
	if w.committedAt.IsZero() {
		return elapsed
	}
	return w.committedAt.Sub(started)
}

// statusCode reports the status the response carried: 200 when the handler
// returned without writing, as net/http then sends.
func (w *trackedResponseWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *trackedResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

type trackedFlusher struct {
	*trackedResponseWriter
	flusher http.Flusher
}

func (w *trackedFlusher) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}

// FlushError lets http.ResponseController report a failed flush through the
// wrapper instead of falling back to the error-free Flush.
func (w *trackedFlusher) FlushError() error {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return flushReportingError(w.ResponseWriter, w.flusher)
}

func flushReportingError(w http.ResponseWriter, flusher http.Flusher) error {
	err := http.NewResponseController(w).Flush()
	if errors.Is(err, http.ErrNotSupported) {
		flusher.Flush()
		return nil
	}
	return err
}

type trackedHijacker struct {
	*trackedResponseWriter
	hijacker http.Hijacker
}

func (w *trackedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.hijacker.Hijack()
	if err == nil {
		w.commit(http.StatusSwitchingProtocols)
	}
	return conn, rw, err
}

type trackedFlusherHijacker struct {
	*trackedResponseWriter
	flusher  http.Flusher
	hijacker http.Hijacker
}

func (w *trackedFlusherHijacker) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	w.flusher.Flush()
}

func (w *trackedFlusherHijacker) FlushError() error {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return flushReportingError(w.ResponseWriter, w.flusher)
}

func (w *trackedFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.hijacker.Hijack()
	if err == nil {
		w.commit(http.StatusSwitchingProtocols)
	}
	return conn, rw, err
}

func trackResponseWriter(w http.ResponseWriter) (*trackedResponseWriter, http.ResponseWriter) {
	tracked := &trackedResponseWriter{ResponseWriter: w}
	flusher, canFlush := w.(http.Flusher)
	hijacker, canHijack := w.(http.Hijacker)
	switch {
	case canFlush && canHijack:
		return tracked, &trackedFlusherHijacker{trackedResponseWriter: tracked, flusher: flusher, hijacker: hijacker}
	case canFlush:
		return tracked, &trackedFlusher{trackedResponseWriter: tracked, flusher: flusher}
	case canHijack:
		return tracked, &trackedHijacker{trackedResponseWriter: tracked, hijacker: hijacker}
	default:
		return tracked, tracked
	}
}
