package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noFlushWriter is a ResponseWriter with no flush support at all.
type noFlushWriter struct{ header http.Header }

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *noFlushWriter) WriteHeader(int)             {}

// failingFlushWriter reports a flush failure the way net/http's own writer
// does once the client connection is gone.
type failingFlushWriter struct {
	noFlushWriter
	err     error
	flushes int
}

func (w *failingFlushWriter) FlushError() error {
	w.flushes++
	return w.err
}

// baseURLWrapped reaches the wrapper the way production does: through
// Server.applyBaseURL for a request under the configured prefix.
func baseURLWrapped(t *testing.T, underlying http.ResponseWriter) http.ResponseWriter {
	t.Helper()
	base, err := parseBaseURL("https://media.example/beamfall")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{baseURL: base}
	req := httptest.NewRequest(http.MethodGet, "/beamfall/api/events", nil)
	w, r, ok := srv.applyBaseURL(underlying, req)
	if !ok {
		t.Fatal("applyBaseURL rejected a prefixed request")
	}
	if _, wrapped := w.(*baseURLResponseWriter); !wrapped {
		t.Fatalf("applyBaseURL returned %T, want the base-URL wrapper", w)
	}
	if r.URL.Path != "/api/events" {
		t.Fatalf("applyBaseURL path = %q, want the prefix stripped", r.URL.Path)
	}
	return w
}

func TestBaseURLWriterFlushReachesSupportedWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	w := baseURLWrapped(t, rec)
	if err := http.NewResponseController(w).Flush(); err != nil {
		t.Fatalf("controller Flush over a flushable writer = %v, want nil", err)
	}
	if !rec.Flushed {
		t.Fatal("controller Flush did not reach the underlying writer")
	}

	rec = httptest.NewRecorder()
	w = baseURLWrapped(t, rec)
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatalf("wrapper %T is no longer an http.Flusher", w)
	}
	flusher.Flush()
	if !rec.Flushed {
		t.Fatal("http.Flusher Flush did not reach the underlying writer")
	}
}

func TestBaseURLWriterFlushReportsUnsupportedWriter(t *testing.T) {
	underlying := &noFlushWriter{header: http.Header{}}
	if err := http.NewResponseController(underlying).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("fixture: bare writer Flush = %v, want ErrNotSupported", err)
	}
	w := baseURLWrapped(t, underlying)
	if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("controller Flush through the wrapper = %v, want ErrNotSupported", err)
	}
	// http.Flusher has no error return; calling it must stay harmless.
	w.(http.Flusher).Flush()
}

func TestBaseURLWriterFlushReportsUnderlyingError(t *testing.T) {
	cause := errors.New("client went away")
	underlying := &failingFlushWriter{noFlushWriter: noFlushWriter{header: http.Header{}}, err: cause}
	w := baseURLWrapped(t, underlying)
	if err := http.NewResponseController(w).Flush(); !errors.Is(err, cause) {
		t.Fatalf("controller Flush through the wrapper = %v, want %v", err, cause)
	}
	w.(http.Flusher).Flush()
	if underlying.flushes != 2 {
		t.Fatalf("underlying flushes = %d, want 2 (controller and http.Flusher)", underlying.flushes)
	}
}
