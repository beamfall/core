package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

const v1TestCapabilityProfile = `{"capabilityProfile":{"client":"beamfall-test","containers":["mp4"],"directCodecs":["h264"]}}`

// v1DeviceRequest sends a /v1 request with an enrolled device Session's header
// credential, or with a bearer credential when bearer is non-empty.
func v1DeviceRequest(t *testing.T, srv *Server, method, path, body, bearer string, authorize func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := newJSONRequest(method, path, bytes.NewReader([]byte(body)))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if authorize != nil {
		authorize(req)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestV1ClientRoutesAcceptOnlyEnrolledDeviceSessions pins ADR-0234 D1 for the
// client routes: a device Session passes, while a scoped key (even one holding
// every scope) and an admin API token get 403 and no credential gets 401.
func TestV1ClientRoutesAcceptOnlyEnrolledDeviceSessions(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	allScopes := mintKey(t, st, "all-scopes", "profile-default",
		store.ScopeReadCatalog, store.ScopeReadSessions, store.ScopePlaybackControl, store.ScopeIngest, store.ScopeBackup)
	_, adminToken, err := st.CreateAPIToken(context.Background(), "ops", time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	routes := []struct {
		method, pattern, body string
	}{
		{http.MethodPost, "/v1/media/{mediaID}/playback-info", v1TestCapabilityProfile},
		{http.MethodPut, "/v1/media/{mediaID}/progress", `{"positionSeconds":10}`},
	}
	for _, route := range routes {
		path := strings.Replace(route.pattern, "{mediaID}", "media-alpha", 1)
		if rec := v1DeviceRequest(t, srv, route.method, path, route.body, "", authorizeViewer); rec.Code != http.StatusOK {
			t.Errorf("device session %s %s = %d, want 200: %s", route.method, path, rec.Code, rec.Body.String())
		}
		for name, bearer := range map[string]string{"scoped key": allScopes, "admin token": adminToken} {
			if rec := v1DeviceRequest(t, srv, route.method, path, route.body, bearer, nil); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403: %s", name, route.method, path, rec.Code, rec.Body.String())
			}
		}
		if rec := v1DeviceRequest(t, srv, route.method, path, route.body, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("no credential %s %s = %d, want 401: %s", route.method, path, rec.Code, rec.Body.String())
		}
	}
}

// TestV1CatalogReadsAcceptDeviceSessions pins the browse and detail reads for
// a device Session, filtered by its Profile like a key bound to that Profile.
func TestV1CatalogReadsAcceptDeviceSessions(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/v1/libraries", "/v1/libraries/library-local/media", "/v1/media/media-alpha"} {
		if rec := v1DeviceRequest(t, srv, http.MethodGet, path, "", "", authorizeManaged); rec.Code != http.StatusOK {
			t.Errorf("device session GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := v1DeviceRequest(t, srv, http.MethodGet, "/v1/media/media-archive", "", "", authorizeManaged); rec.Code != http.StatusNotFound {
		t.Errorf("managed device session GET hidden media = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestV1AutomationRoutesRejectDeviceSessions keeps the automation routes
// scoped-key only (ADR-0234 D1).
func TestV1AutomationRoutesRejectDeviceSessions(t *testing.T) {
	srv := newTestServer(t)
	for _, route := range v1RouteContracts {
		if slices.Contains(route.Credentials, V1CredentialDeviceSession) {
			continue
		}
		path := strings.NewReplacer("{libraryID}", "library-local", "{mediaID}", "media-alpha").Replace(route.Pattern)
		rec := v1DeviceRequest(t, srv, route.Method, path, `{}`, "", authorizeViewer)
		if rec.Code != http.StatusForbidden {
			t.Errorf("device session %s %s = %d, want 403: %s", route.Method, route.Pattern, rec.Code, rec.Body.String())
		}
	}
}

// TestV1PlaybackInfoProjectsTheDecision checks the frozen projection: a direct
// stream whose URL carries its own token, and the Media duration.
func TestV1PlaybackInfoProjectsTheDecision(t *testing.T) {
	srv := newTestServerWithMediaFile(t, "media-alpha", []byte("beamfall file bytes"))
	rec := v1DeviceRequest(t, srv, http.MethodPost, "/v1/media/media-alpha/playback-info", v1TestCapabilityProfile, "", authorizeViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp v1PlaybackInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MediaID != "media-alpha" || resp.Decision == "" || resp.Decision == "unavailable" {
		t.Fatalf("response = %+v, want a playable decision for media-alpha", resp)
	}
	if len(resp.Streams) == 0 || resp.Streams[0].URL == "" {
		t.Fatalf("streams = %+v, want at least one stream with a URL", resp.Streams)
	}
	if resp.DurationSeconds <= 0 {
		t.Fatalf("durationSeconds = %d, want the Media duration", resp.DurationSeconds)
	}
	// The stream URL plays without any other credential.
	streamRec := httptest.NewRecorder()
	srv.ServeHTTP(streamRec, httptest.NewRequest(http.MethodGet, resp.Streams[0].URL, nil))
	if streamRec.Code != http.StatusOK || streamRec.Body.String() != "beamfall file bytes" {
		t.Fatalf("stream GET = %d %q, want 200 with the file bytes", streamRec.Code, streamRec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for field := range raw {
		switch field {
		case "mediaID", "decision", "reason", "streamTokenRequired", "streams", "durationSeconds", "positionSeconds", "captionTracks":
		default:
			t.Errorf("response carries unprojected field %q", field)
		}
	}
}

func TestV1PlaybackInfoRejectsHiddenAndMalformedRequests(t *testing.T) {
	srv := newTestServer(t)
	for _, id := range []string{"media-archive", "media-missing"} {
		rec := v1DeviceRequest(t, srv, http.MethodPost, "/v1/media/"+id+"/playback-info", v1TestCapabilityProfile, "", authorizeViewer)
		if rec.Code != http.StatusNotFound {
			t.Errorf("playback-info %s = %d, want 404: %s", id, rec.Code, rec.Body.String())
		}
	}
	if rec := v1DeviceRequest(t, srv, http.MethodPost, "/v1/media/media-alpha/playback-info", `{"capabilityProfile":`, "", authorizeViewer); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestV1ReportProgressRecordsPositionAndWatched checks the resume write and the
// watched transition the first-party clients use.
func TestV1ReportProgressRecordsPositionAndWatched(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()

	rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", `{"positionSeconds":42,"eventTimeMS":1000}`, "", authorizeViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("progress status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp v1ProgressResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.MediaID != "media-alpha" || resp.PositionSeconds != 42 || resp.Watched {
		t.Fatalf("response = %+v, want position 42, not watched", resp)
	}
	stored, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil || !ok || stored.PositionSeconds != 42 {
		t.Fatalf("stored progress = %+v ok=%v err=%v, want position 42", stored, ok, err)
	}

	rec = v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", `{"positionSeconds":42,"finished":true,"eventTimeMS":2000}`, "", authorizeViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("finished status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Watched {
		t.Fatalf("response = %+v, want watched after finished", resp)
	}
}

func TestV1ReportProgressRejectsHiddenAndNegativeReports(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()

	if rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-archive/progress", `{"positionSeconds":5}`, "", authorizeViewer); rec.Code != http.StatusNotFound {
		t.Errorf("hidden media progress = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-archive"); err != nil || ok {
		t.Errorf("hidden media progress stored: ok=%v err=%v, want nothing written", ok, err)
	}
	for _, body := range []string{`{"positionSeconds":-1}`, `{"positionSeconds":1,"durationSeconds":-1}`, `{"positionSeconds":1,"eventTimeMS":-1}`} {
		if rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", body, "", authorizeViewer); rec.Code != http.StatusBadRequest {
			t.Errorf("progress %s = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || ok {
		t.Errorf("rejected progress stored: ok=%v err=%v, want nothing written", ok, err)
	}
}
