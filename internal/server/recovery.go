package server

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
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
				"path", redactTokenPath(r.URL.Path),
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

// redactTokenPath replaces the token segment of a token-bearing path, keeping
// the route prefix and the non-secret trailing segments as safe diagnostics.
// Paths outside the token prefixes pass through unchanged.
func redactTokenPath(path string) string {
	for _, prefix := range tokenRoutePrefixes {
		rest, found := strings.CutPrefix(path, prefix)
		if !found || rest == "" {
			continue
		}
		_, suffix, hasSuffix := strings.Cut(rest, "/")
		if !hasSuffix {
			return prefix + redactedTokenSegment
		}
		return prefix + redactedTokenSegment + "/" + suffix
	}
	return path
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
}

func (w *trackedResponseWriter) WriteHeader(status int) {
	w.ResponseWriter.WriteHeader(status)
	// Informational responses do not commit the final response, except for the
	// protocol-switching 101 response.
	if status == http.StatusSwitchingProtocols || status >= http.StatusOK {
		w.committed = true
	}
}

func (w *trackedResponseWriter) Write(p []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(p)
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

type trackedHijacker struct {
	*trackedResponseWriter
	hijacker http.Hijacker
}

func (w *trackedHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.hijacker.Hijack()
	if err == nil {
		w.committed = true
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

func (w *trackedFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.hijacker.Hijack()
	if err == nil {
		w.committed = true
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
