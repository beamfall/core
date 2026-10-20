package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestPlaybackRoutesMapClientErrors(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/api/playback-info", "/api/preview-info"} {
		req := newJSONRequest(http.MethodPost, path, strings.NewReader(`{}`))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400: %s", path, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, `"error":"mediaID is required"`) {
			t.Fatalf("%s body = %q, want safe validation message", path, body)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/playback-info", nil)
	for _, tc := range []struct {
		err    error
		status int
		body   string
	}{
		{playback.ErrInvalidSelector, http.StatusBadRequest, "invalid selector"},
		{playback.ErrNotFound, http.StatusNotFound, "media unavailable"},
	} {
		rec := httptest.NewRecorder()
		writePlaybackError(rec, req, "playback info unavailable", tc.err)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) {
			t.Fatalf("error %v mapped to status/body %d/%q, want %d containing %q", tc.err, rec.Code, rec.Body.String(), tc.status, tc.body)
		}
	}
}

func TestPlaybackRoutesHideInternalStoreErrorsAndLogRequestID(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	srv, _ := newTestServerWithStore(t)
	broken := storetest.Open(t)
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	srv.playback = playback.NewService(broken, streamtoken.NewService([]byte("test-token-key"), clock), clock)

	for _, tc := range []struct {
		path string
		body string
		want string
	}{
		{"/api/playback-info", `{"mediaID":"media-alpha"}`, "playback info unavailable"},
		{"/api/preview-info", `{"mediaID":"media-alpha"}`, "preview info unavailable"},
	} {
		logs.Reset()
		req := newJSONRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		authorizeViewer(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s status = %d, want 500: %s", tc.path, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.want) || strings.Contains(body, "store is closed") {
			t.Fatalf("%s body leaked internal error or omitted generic message: %q", tc.path, body)
		}
		requestID := rec.Header().Get(requestIDHeader)
		if requestID == "" || !strings.Contains(logs.String(), "requestID="+requestID) {
			t.Fatalf("%s log missing request correlation %q: %s", tc.path, requestID, logs.String())
		}
		if !strings.Contains(logs.String(), "store is closed") {
			t.Fatalf("%s log omitted private cause: %s", tc.path, logs.String())
		}
	}
}
