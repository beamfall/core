package server

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/playback"
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
		rec := v1DeviceRequest(t, srv, http.MethodGet, path, "", "", authorizeManaged)
		if rec.Code != http.StatusOK {
			t.Errorf("device session GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
		// Shared caches do not key on the header credential.
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", path, got)
		}
		head := v1DeviceRequest(t, srv, http.MethodHead, path, "", "", authorizeManaged)
		if got := head.Header().Get("Cache-Control"); head.Code != http.StatusOK || got != "no-store" {
			t.Errorf("HEAD %s = %d, Cache-Control %q; want 200 and no-store", path, head.Code, got)
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

// TestV1ReportProgressRecordsPlaybackID passes the client's playback ID and
// paused state through to the progress event, as GraphQL reportPlayback does,
// so play history can tell two devices' concurrent plays apart (CORE-1703).
// An ID with surrounding whitespace or over 64 characters is refused.
func TestV1ReportProgressRecordsPlaybackID(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", `{"positionSeconds":42,"eventTimeMS":1000,"playbackID":"play-v1","paused":true,"playedMS":41500}`, "", authorizeViewer)
	if rec.Code != http.StatusOK {
		t.Fatalf("progress status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	export, err := st.ExportMyEngagement(context.Background(), "profile-default", store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range export.EngagementEvents {
		var payload map[string]any
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["operation"] == "progress" {
			found = payload["playbackId"] == "play-v1" && payload["paused"] == true && payload["playedMs"] == float64(41500)
		}
	}
	if !found {
		t.Fatalf("progress events = %#v, want playbackId play-v1, paused and playedMs 41500", export.EngagementEvents)
	}
	for _, id := range []string{" play-v1", strings.Repeat("x", 65)} {
		body, _ := json.Marshal(map[string]any{"positionSeconds": 50, "playbackID": id})
		if rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", string(body), "", authorizeViewer); rec.Code != http.StatusBadRequest {
			t.Errorf("progress with playbackID %q = %d, want 400: %s", id, rec.Code, rec.Body.String())
		}
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
	for _, body := range []string{`{}`, `{"positionSeconds":null}`, `{"positionSeconds":-1}`, `{"positionSeconds":1,"durationSeconds":-1}`, `{"positionSeconds":1,"eventTimeMS":-1}`, `{"positionSeconds":1,"playedMS":-1}`} {
		if rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", body, "", authorizeViewer); rec.Code != http.StatusBadRequest {
			t.Errorf("progress %s = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || ok {
		t.Errorf("rejected progress stored: ok=%v err=%v, want nothing written", ok, err)
	}
}

// TestV1PreflightAllowsDeviceSessionHeaders lets a browser client send the
// device Session headers and the progress PUT cross-origin.
func TestV1PreflightAllowsDeviceSessionHeaders(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodOptions, "/v1/media/media-alpha/progress", nil)
	req.Header.Set("Origin", "https://client.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPut)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPut) {
		t.Errorf("Access-Control-Allow-Methods = %q, want PUT", got)
	}
	allowed := rec.Header().Get("Access-Control-Allow-Headers")
	for _, header := range []string{auth.SessionHeader, auth.DeviceHeader, auth.DeviceSecretHeader} {
		if !strings.Contains(allowed, header) {
			t.Errorf("Access-Control-Allow-Headers = %q, want %s", allowed, header)
		}
	}
}

// TestV1PlaybackInfoKeepsCaptionStreamIndex keeps the stream index of a muxed
// subtitle track, with or without a URL to fetch.
func TestV1PlaybackInfoKeepsCaptionStreamIndex(t *testing.T) {
	index := 3
	out := v1PlaybackInfoFrom(playback.InfoResponse{CaptionTracks: []playback.CaptionTrack{
		{ID: "muxed", Format: "pgs", StreamIndex: &index},
		{ID: "sidecar", Format: "vtt", URL: "/api/media/subtitle/x"},
		{ID: "muxed-text", Format: "vtt", URL: "/api/media/subtitle/y", StreamIndex: &index},
	}})
	if len(out.CaptionTracks) != 3 {
		t.Fatalf("captionTracks = %+v, want 3", out.CaptionTracks)
	}
	if got := out.CaptionTracks[0].StreamIndex; got == nil || *got != 3 {
		t.Errorf("muxed streamIndex = %v, want 3", got)
	}
	if out.CaptionTracks[1].URL == "" || out.CaptionTracks[1].StreamIndex != nil {
		t.Errorf("sidecar track = %+v, want URL and no streamIndex", out.CaptionTracks[1])
	}
	if got := out.CaptionTracks[2]; got.URL == "" || got.StreamIndex == nil || *got.StreamIndex != 3 {
		t.Errorf("muxed text track = %+v, want both URL and streamIndex", got)
	}
}

// TestOpenAPIProgressRequestMatchesHandler pins the documented /v1
// ProgressRequest schema to the fields the handler decodes: each property's
// name and JSON type, and which ones are required, so a client generated from
// the served contract sends what the handler reads (Codex CORE-1703 r5 and r6).
func TestOpenAPIProgressRequestMatchesHandler(t *testing.T) {
	raw, err := os.ReadFile("../../docs/api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schema := spec.Components.Schemas["ProgressRequest"]
	documented := map[string]string{}
	for name, property := range schema.Properties {
		documented[name] = property.Type
	}
	decoded := map[string]string{}
	var required []string
	for field := range reflect.TypeFor[v1ProgressRequest]().Fields() {
		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		kind := field.Type
		if kind.Kind() == reflect.Pointer {
			kind = kind.Elem()
		}
		switch kind.Kind() {
		case reflect.Int, reflect.Int64:
			decoded[name] = "integer"
		case reflect.Bool:
			decoded[name] = "boolean"
		case reflect.String:
			decoded[name] = "string"
		default:
			t.Fatalf("field %s has type %s, which this test does not map to JSON", field.Name, field.Type)
		}
		if !slices.Contains(strings.Split(options, ","), "omitempty") {
			required = append(required, name)
		}
	}
	if !maps.Equal(documented, decoded) {
		t.Fatalf("OpenAPI ProgressRequest properties = %v, handler decodes %v", documented, decoded)
	}
	slices.Sort(schema.Required)
	slices.Sort(required)
	if !slices.Equal(schema.Required, required) {
		t.Fatalf("OpenAPI ProgressRequest required = %v, handler requires %v", schema.Required, required)
	}
}
