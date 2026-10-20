package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func handleRecoveryTestRoute(srv *Server, pattern string, handler http.HandlerFunc) {
	srv.accessClasses[pattern] = AccessInternalOnly
	srv.mux.HandleFunc(pattern, handler)
}

func TestServeHTTPRecoversPanicWithCorrelatedJSONError(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := newTestServer(t)
	handleRecoveryTestRoute(srv, "GET /panic-before-write", func(http.ResponseWriter, *http.Request) {
		panic("handler exploded")
	})
	req := httptest.NewRequest(http.MethodGet, "/panic-before-write?token=secret", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", contentType)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"internal server error"}` {
		t.Fatalf("body = %q, want JSON internal error", got)
	}

	reqID := rec.Header().Get(requestIDHeader)
	if reqID == "" {
		t.Fatal("response missing request ID")
	}
	logOut := logs.String()
	for _, want := range []string{"http request panic", "panic=\"handler exploded\"", "stack=", "goroutine", "requestID=" + reqID} {
		if !strings.Contains(logOut, want) {
			t.Fatalf("log = %q, want %q", logOut, want)
		}
	}
	if strings.Contains(logOut, "token=secret") {
		t.Fatalf("log leaked query: %q", logOut)
	}
}

// A panicking token route must not log the URL-carried Stream Token: the panic
// line keeps the matched route, the redacted path shape, and the request ID —
// the safe diagnostics — while the credential segment never reaches the log.
func TestServeHTTPPanicLogRedactsTokenBearingPaths(t *testing.T) {
	const canary = "canary-stream-token-0xDEAD"
	cases := []struct {
		pattern  string
		path     string
		wantPath string
	}{
		{"GET /api/media/direct/{token}", "/api/media/direct/" + canary, "/api/media/direct/[redacted]"},
		{"GET /api/media/hls/{token}/{name}", "/api/media/hls/" + canary + "/index.m3u8", "/api/media/hls/[redacted]/index.m3u8"},
		{"GET /api/media/assets/{token}/{name}", "/api/media/assets/" + canary + "/sprite.jpg", "/api/media/assets/[redacted]/sprite.jpg"},
		{"GET /api/media/subtitle/{token}/{name}", "/api/media/subtitle/" + canary + "/3.vtt", "/api/media/subtitle/[redacted]/3.vtt"},
		{"GET /api/media/sidecar/{token}/{name}", "/api/media/sidecar/" + canary + "/0.vtt", "/api/media/sidecar/[redacted]/0.vtt"},
		{"GET /api/media/artwork-candidate/{token}/{name}", "/api/media/artwork-candidate/" + canary + "/cand-1", "/api/media/artwork-candidate/[redacted]/cand-1"},
		{"GET /api/media/proxy/{token}", "/api/media/proxy/" + canary, "/api/media/proxy/[redacted]"},
		{"GET /api/pack/manifest/{token}", "/api/pack/manifest/" + canary, "/api/pack/manifest/[redacted]"},
		{"GET /api/pack/content/{token}/{name}", "/api/pack/content/" + canary + "/media-alpha", "/api/pack/content/[redacted]/media-alpha"},
		{"GET /api/pack/key/{token}", "/api/pack/key/" + canary, "/api/pack/key/[redacted]"},
		{"GET /api/pack/status/{token}", "/api/pack/status/" + canary, "/api/pack/status/[redacted]"},
	}

	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := newTestServer(t)
	for _, tt := range cases {
		handleRecoveryTestRoute(srv, tt.pattern, func(http.ResponseWriter, *http.Request) {
			panic("token route exploded")
		})
	}

	for _, tt := range cases {
		logs.Reset()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want %d", tt.path, rec.Code, http.StatusInternalServerError)
		}
		logOut := logs.String()
		if strings.Contains(logOut, canary) {
			t.Fatalf("%s: panic log leaked the token: %q", tt.path, logOut)
		}
		if !strings.Contains(logOut, "path="+tt.wantPath) {
			t.Fatalf("%s: panic log lost the redacted path %q: %q", tt.path, tt.wantPath, logOut)
		}
		routePattern := strings.TrimPrefix(tt.pattern, "GET ")
		if !strings.Contains(logOut, routePattern) {
			t.Fatalf("%s: panic log lost the matched route %q: %q", tt.path, routePattern, logOut)
		}
		reqID := rec.Header().Get(requestIDHeader)
		if reqID == "" || !strings.Contains(logOut, "requestID="+reqID) {
			t.Fatalf("%s: panic log lost the request ID %q: %q", tt.path, reqID, logOut)
		}
	}
}

// Paths outside the token-bearing prefixes keep their full path in the panic
// log — redaction is scoped to credential segments, not diagnostics at large.
func TestServeHTTPPanicLogKeepsNonTokenPaths(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv := newTestServer(t)
	handleRecoveryTestRoute(srv, "GET /api/media/artwork/{media}/{slot}", func(http.ResponseWriter, *http.Request) {
		panic("artwork route exploded")
	})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/media/artwork/media-alpha/poster", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(logs.String(), "path=/api/media/artwork/media-alpha/poster") {
		t.Fatalf("panic log lost the non-token path: %q", logs.String())
	}
}

func TestServeHTTPPanicAfterCommitDoesNotRewriteResponse(t *testing.T) {
	srv := newTestServer(t)
	handleRecoveryTestRoute(srv, "GET /panic-after-write", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("partial response"))
		panic(errors.New("late failure"))
	})
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/panic-after-write", nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := rec.Body.String(); got != "partial response" {
		t.Fatalf("body = %q, want committed partial response unchanged", got)
	}
}

func TestServeHTTPPanicRestoresBoundaryHeadersBeforeJSONError(t *testing.T) {
	srv := newTestServer(t)
	handleRecoveryTestRoute(srv, "GET /panic-stale-headers", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "999")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Handler-State", "stale")
		panic("bad headers")
	})
	req := httptest.NewRequest(http.MethodGet, "/panic-stale-headers", nil)
	req.Host = "beamfall.test"
	req.Header.Set("Origin", "https://beamfall.test")
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"internal server error"}` {
		t.Fatalf("body = %q, want unencoded JSON internal error", got)
	}
	for _, stale := range []string{"Content-Length", "Content-Encoding", "X-Handler-State"} {
		if got := rec.Header().Get(stale); got != "" {
			t.Fatalf("%s = %q, want handler-mutated header cleared", stale, got)
		}
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://beamfall.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want boundary CORS header preserved", got)
	}
	if got := rec.Header().Get(requestIDHeader); got == "" {
		t.Fatal("request ID boundary header was not preserved")
	}
}

func TestServeHTTPRepanicsErrAbortHandler(t *testing.T) {
	srv := newTestServer(t)
	handleRecoveryTestRoute(srv, "GET /abort", func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})

	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("recovered panic = %v, want http.ErrAbortHandler", got)
		}
	}()
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
}
