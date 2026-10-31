package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// hijackPushWriter is a minimal ResponseWriter that supports Hijack and Push
// but not Flush, recording what the wrappers forward to it.
type hijackPushWriter struct {
	header    http.Header
	conn      net.Conn
	hijackErr error
	hijacked  int
	pushed    []string
}

func newHijackPushWriter(t *testing.T) *hijackPushWriter {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	return &hijackPushWriter{header: http.Header{}, conn: server}
}

func (w *hijackPushWriter) Header() http.Header         { return w.header }
func (w *hijackPushWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *hijackPushWriter) WriteHeader(int)             {}

func (w *hijackPushWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked++
	if w.hijackErr != nil {
		return nil, nil, w.hijackErr
	}
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func (w *hijackPushWriter) Push(target string, _ *http.PushOptions) error {
	w.pushed = append(w.pushed, target)
	return nil
}

func prefixedWriter(w http.ResponseWriter) *baseURLResponseWriter {
	return &baseURLResponseWriter{
		ResponseWriter: w,
		base:           &url.URL{Scheme: "https", Host: "media.example", Path: "/beamfall"},
	}
}

func TestBaseURLWriterDelegatesOptionalInterfaces(t *testing.T) {
	rec := httptest.NewRecorder()
	w := prefixedWriter(rec)
	if w.Unwrap() != rec {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}
	w.Flush()
	if !rec.Flushed {
		t.Fatal("Flush was not forwarded to the underlying writer")
	}
	if _, _, err := w.Hijack(); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Hijack on a non-hijackable writer = %v, want ErrNotSupported", err)
	}
	if err := w.Push("/web/app.js", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("Push on a non-pusher writer = %v, want ErrNotSupported", err)
	}

	inner := newHijackPushWriter(t)
	w = prefixedWriter(inner)
	conn, _, err := w.Hijack()
	if err != nil || conn != inner.conn || inner.hijacked != 1 {
		t.Fatalf("Hijack = (%v, %v) after %d calls, want the underlying connection", conn, err, inner.hijacked)
	}
	for _, target := range []string{"/web/app.js", "/beamfall/web/app.css", "relative.js"} {
		if err := w.Push(target, nil); err != nil {
			t.Fatalf("Push(%q): %v", target, err)
		}
	}
	want := []string{"/beamfall/web/app.js", "/beamfall/web/app.css", "relative.js"}
	if strings.Join(inner.pushed, ",") != strings.Join(want, ",") {
		t.Fatalf("pushed targets = %v, want %v (absolute paths gain the base prefix once)", inner.pushed, want)
	}
}

func TestTrackedHijackerCommitsOnlyOnSuccessfulHijack(t *testing.T) {
	inner := newHijackPushWriter(t)
	inner.hijackErr = errors.New("hijack refused")
	tracked, wrapped := trackResponseWriter(inner)
	hijacker, ok := wrapped.(http.Hijacker)
	if !ok {
		t.Fatalf("wrapped writer %T dropped the Hijacker interface", wrapped)
	}
	if _, ok := wrapped.(http.Flusher); ok {
		t.Fatalf("wrapped writer %T invented a Flusher the underlying writer lacks", wrapped)
	}
	if _, _, err := hijacker.Hijack(); err == nil {
		t.Fatal("Hijack swallowed the underlying error")
	}
	if tracked.committed {
		t.Fatal("failed Hijack marked the response committed")
	}

	inner.hijackErr = nil
	conn, _, err := hijacker.Hijack()
	if err != nil || conn != inner.conn {
		t.Fatalf("Hijack = (%v, %v), want the underlying connection", conn, err)
	}
	if !tracked.committed {
		t.Fatal("successful Hijack did not mark the response committed, so recovery could write over the connection")
	}
}

func TestEventOutboxReadErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("outbox offline")
	err := error(&eventOutboxReadError{err: cause})
	if !errors.Is(err, cause) {
		t.Fatal("eventOutboxReadError does not unwrap to its cause")
	}
	if !strings.Contains(err.Error(), "outbox offline") {
		t.Fatalf("Error() = %q, want the cause text", err.Error())
	}
}

func TestKnownProxiesStringRoundTrips(t *testing.T) {
	proxies, err := ParseKnownProxies("10.0.0.0/8, 192.168.1.1/32", true)
	if err != nil {
		t.Fatalf("ParseKnownProxies: %v", err)
	}
	text := proxies.String()
	if !strings.Contains(text, "10.0.0.0/8") || strings.Count(text, ",") != 1 {
		t.Fatalf("String() = %q, want both prefixes comma-joined", text)
	}
	again, err := ParseKnownProxies(text, true)
	if err != nil || again.String() != text {
		t.Fatalf("re-parsed String() = (%v, %v), want %q", again, err, text)
	}
}

func TestBadTestFaultMessageIsClientSafe(t *testing.T) {
	var msg clientMessage
	if !errors.As(errBadTestFault, &msg) || errBadTestFault.Error() != "method and absolute path are required" {
		t.Fatalf("errBadTestFault = %q, want the client message", errBadTestFault)
	}
}

func TestRequestFromRelayReadsOnlyTheProcessMarker(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("X-Beamfall-Relayed", "true")
	if RequestFromRelay(req) {
		t.Fatal("a client header forged the relay marker")
	}
	relayed := req.WithContext(ContextWithRelayedRequest(context.Background()))
	if !RequestFromRelay(relayed) {
		t.Fatal("relay-marked request not reported as relayed")
	}
}

func TestPackStreamRoutesDispatchAndDegrade(t *testing.T) {
	srv := newTestServer(t)
	paths := map[string]string{
		"manifest": "/api/pack/manifest/token-1",
		"key":      "/api/pack/key/token-1",
		"status":   "/api/pack/status/token-1",
	}
	for name, path := range paths {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "offline pack unavailable") {
			t.Fatalf("unwired %s status = %d body=%s, want 503", name, rec.Code, rec.Body.String())
		}
	}

	hits := map[string]int{}
	handler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			hits[name]++
			w.WriteHeader(http.StatusOK)
		}
	}
	srv.SetPackHandlers(nil, handler("manifest"), nil, handler("key"), handler("status"))
	for name, path := range paths {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || hits[name] != 1 {
			t.Fatalf("wired %s status = %d hits=%v, want 200 and exactly its own handler", name, rec.Code, hits)
		}
	}
}

func TestBeamOfferRouteMapsFailures(t *testing.T) {
	srv := newTestServer(t)
	offer := func(body string, authorize func(*http.Request)) *httptest.ResponseRecorder {
		req := newJSONRequest(http.MethodPost, "/api/beam/offers", bytes.NewReader([]byte(body)))
		if authorize != nil {
			authorize(req)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	tests := []struct {
		name      string
		body      string
		authorize func(*http.Request)
		want      int
		wantBody  string
	}{
		{name: "no session", body: `{"mediaID":"media-alpha"}`, want: http.StatusUnauthorized, wantBody: "invalid session"},
		{name: "malformed JSON", body: `{"mediaID":`, authorize: authorizeViewer, want: http.StatusBadRequest},
		{name: "negative position", body: `{"mediaID":"media-alpha","positionSeconds":-1}`, authorize: authorizeViewer, want: http.StatusBadRequest, wantBody: "invalid beam handoff request"},
		{name: "unknown target", body: `{"mediaID":"media-alpha","targetDeviceID":"device-missing"}`, authorize: authorizeViewer, want: http.StatusBadRequest, wantBody: "invalid beam handoff request"},
		{name: "unknown media", body: `{"mediaID":"media-missing"}`, authorize: authorizeViewer, want: http.StatusNotFound, wantBody: "media unavailable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := offer(tc.body, tc.authorize)
			if rec.Code != tc.want || !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("status = %d body=%s, want %d containing %q", rec.Code, rec.Body.String(), tc.want, tc.wantBody)
			}
		})
	}

	srv.beam = nil
	rec := offer(`{"mediaID":"media-alpha"}`, authorizeViewer)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "beam handoff unavailable") {
		t.Fatalf("unwired beam status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
}
