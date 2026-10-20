package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

func testV1ScanEnqueuer(st *store.Store) V1ScanEnqueuer {
	return func(ctx context.Context, libraryID store.LibraryID, root string) (store.JobID, error) {
		job, err := scan.NewJob(scan.JobRequest{LibraryID: libraryID, Root: root, CreatedAt: time.Now().UTC()})
		if err != nil {
			return "", err
		}
		created, err := st.EnqueueJob(ctx, job)
		if err != nil {
			return "", err
		}
		return created.ID, nil
	}
}

// mintKey creates a scoped developer API key on the test store and returns the
// full bfk_ string for the Authorization header. profileID "" mints an
// admin-scoped (unbound) key.
func mintKey(t *testing.T, st *store.Store, name string, profileID store.ProfileID, scopes ...store.APIScope) string {
	t.Helper()
	_, key, err := st.CreateScopedAPIKey(context.Background(), name, scopes, profileID, time.Now().UTC())
	if err != nil {
		t.Fatalf("CreateScopedAPIKey(%s): %v", name, err)
	}
	return key
}

func v1Get(t *testing.T, srv *Server, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func v1Post(t *testing.T, srv *Server, path, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newJSONRequest(http.MethodPost, path, bytes.NewReader([]byte(body)))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestV1RequiresMatchingScope pins per-route scope enforcement: a read-catalog
// key reads the catalog, an ingest-only key is 403 on it, and an ingest key can
// hit the scan trigger but not the catalog. A session credential (no API
// scopes) is rejected from every /v1/ route.
func TestV1RequiresMatchingScope(t *testing.T) {
	srv, st := newTestServerWithStore(t)

	catalogKey := mintKey(t, st, "catalog", "", store.ScopeReadCatalog)
	ingestKey := mintKey(t, st, "ingest", "", store.ScopeIngest)

	// read-catalog key reads /v1/libraries.
	if rec := v1Get(t, srv, "/v1/libraries", catalogKey); rec.Code != http.StatusOK {
		t.Fatalf("catalog key GET /v1/libraries = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// ingest-only key is forbidden on the catalog read.
	if rec := v1Get(t, srv, "/v1/libraries", ingestKey); rec.Code != http.StatusForbidden {
		t.Fatalf("ingest key GET /v1/libraries = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// read-catalog key is forbidden on the ingest trigger.
	scanRoot := filepath.Join(t.TempDir(), "scan-root")
	if err := os.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatalf("mkdir scan root: %v", err)
	}
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{scanRoot}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	scanReq := func(key string) *httptest.ResponseRecorder {
		body := bytes.NewReader([]byte(`{"libraryID":"library-local","root":` + strconv.Quote(scanRoot) + `}`))
		req := newJSONRequest(http.MethodPost, "/v1/scans", body)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	if rec := scanReq(catalogKey); rec.Code != http.StatusForbidden {
		t.Fatalf("catalog key POST /v1/scans = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// ingest key hits the scan trigger (202 + a job id).
	rec := scanReq(ingestKey)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("ingest key POST /v1/scans = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var scanResp struct {
		JobIDs []string `json:"jobIDs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &scanResp); err != nil {
		t.Fatalf("decode scan response: %v", err)
	}
	if len(scanResp.JobIDs) != 1 || scanResp.JobIDs[0] == "" {
		t.Fatalf("jobIDs = %#v, want one non-empty id", scanResp.JobIDs)
	}

	// A session credential carries no API scopes — every /v1/ route rejects it.
	req := httptest.NewRequest(http.MethodGet, "/v1/libraries", nil)
	authorizeViewer(req)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("session credential GET /v1/libraries = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// No credential at all is 401.
	if rec := v1Get(t, srv, "/v1/libraries", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential GET /v1/libraries = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestV1ScanRequiresPersistedLibraryRoot(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	scanRoot := filepath.Join(t.TempDir(), "scan-root")
	if err := os.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatalf("mkdir scan root: %v", err)
	}
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{scanRoot}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	key := mintKey(t, st, "ingest", "", store.ScopeIngest)

	otherRoot := filepath.Join(t.TempDir(), "other")
	if rec := v1Post(t, srv, "/v1/scans", key, `{"libraryID":"library-local","root":`+strconv.Quote(otherRoot)+`}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("outside root POST /v1/scans = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	rec := v1Post(t, srv, "/v1/scans", key, `{"libraryID":"library-local","root":`+strconv.Quote(scanRoot)+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("persisted root POST /v1/scans = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestV1EventsRequiresEventsSubscribeScopedKey(t *testing.T) {
	srv, st := newTestServerWithStore(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("session GET /v1/events = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	catalogKey := mintKey(t, st, "catalog", "", store.ScopeReadCatalog)
	if rec := v1Get(t, srv, "/v1/events", catalogKey); rec.Code != http.StatusForbidden {
		t.Fatalf("catalog key GET /v1/events = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	eventKey := mintKey(t, st, "events", "", store.ScopeEventsSubscribe)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req = httptest.NewRequest(http.MethodGet, "/v1/events", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+eventKey)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("event key GET /v1/events = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestV1CatalogIsRevealFiltered pins that a Profile-bound catalog key sees only
// its Profile's Libraries: the managed Profile (profile-managed) is granted only
// library-local, so the restricted library-archive never appears, and a Media
// in it resolves to 404.
func TestV1CatalogIsRevealFiltered(t *testing.T) {
	srv, st := newTestServerWithStore(t)

	// profile-managed: granted only library-local (development seed).
	managedKey := mintKey(t, st, "managed-catalog", "profile-managed", store.ScopeReadCatalog)

	rec := v1Get(t, srv, "/v1/libraries", managedKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var libs struct {
		Libraries []v1LibrarySummary `json:"libraries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &libs); err != nil {
		t.Fatalf("decode libraries: %v", err)
	}
	if len(libs.Libraries) != 1 || libs.Libraries[0].ID != "library-local" {
		t.Fatalf("libraries = %#v, want only library-local", libs.Libraries)
	}

	rec = v1Get(t, srv, "/v1/libraries/library-local/media", managedKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("media list status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var mediaList v1LibraryMediaResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &mediaList); err != nil {
		t.Fatalf("decode media list: %v", err)
	}
	if len(mediaList.Media) == 0 || mediaList.Media[0].LibraryID != "library-local" {
		t.Fatalf("media list = %#v, want non-empty items with libraryID", mediaList.Media)
	}

	// media-archive lives in the restricted library-archive the managed Profile
	// cannot see: the detail read is 404 (hidden and missing stay identical).
	if rec := v1Get(t, srv, "/v1/media/media-archive", managedKey); rec.Code != http.StatusNotFound {
		t.Fatalf("managed key GET /v1/media/media-archive = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	// media-alpha lives in library-local: visible.
	if rec := v1Get(t, srv, "/v1/media/media-alpha", managedKey); rec.Code != http.StatusOK {
		t.Fatalf("managed key GET /v1/media/media-alpha = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestV1SessionsCursorPagination(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(time.Hour)
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken (default): %v", err)
	}
	if err := st.RecordStreamToken(ctx, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken (managed): %v", err)
	}
	key := mintKey(t, st, "sessions-pagination", "", store.ScopeReadSessions)

	first := v1Get(t, srv, "/v1/sessions?limit=1", key)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d, want 200: %s", first.Code, first.Body.String())
	}
	var firstPage v1SessionsResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(firstPage.Sessions) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %#v, want one session and a cursor", firstPage)
	}

	second := v1Get(t, srv, "/v1/sessions?limit=1&cursor="+firstPage.NextCursor, key)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d, want 200: %s", second.Code, second.Body.String())
	}
	var secondPage v1SessionsResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondPage); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(secondPage.Sessions) != 1 || secondPage.Sessions[0].SessionID == firstPage.Sessions[0].SessionID {
		t.Fatalf("second page = %#v, want a distinct session", secondPage)
	}

	invalid := v1Get(t, srv, "/v1/sessions?cursor=not-a-cursor", key)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d, want 400: %s", invalid.Code, invalid.Body.String())
	}
}

// TestV1SessionsProfileBound pins that a Profile-bound read-sessions key sees
// only its own Profile's active sessions, while an admin-scoped key sees all.
func TestV1SessionsProfileBound(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(time.Hour)

	// One active session per Profile (seeded directly into the Stream Token
	// ledger, the live-sessions read view).
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken (default): %v", err)
	}
	if err := st.RecordStreamToken(ctx, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken (managed): %v", err)
	}

	// Profile-bound key (profile-default) sees only its own session.
	boundKey := mintKey(t, st, "bound-sessions", "profile-default", store.ScopeReadSessions)
	rec := v1Get(t, srv, "/v1/sessions", boundKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("bound key status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var bound struct {
		Sessions []v1SessionItem `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bound); err != nil {
		t.Fatalf("decode bound sessions: %v", err)
	}
	if len(bound.Sessions) != 1 || bound.Sessions[0].ProfileID != "profile-default" {
		t.Fatalf("bound sessions = %#v, want only profile-default", bound.Sessions)
	}

	// Admin-scoped key (no Profile binding) sees both sessions.
	adminKey := mintKey(t, st, "admin-sessions", "", store.ScopeReadSessions)
	rec = v1Get(t, srv, "/v1/sessions", adminKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin key status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var all struct {
		Sessions []v1SessionItem `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode admin sessions: %v", err)
	}
	if len(all.Sessions) != 2 {
		t.Fatalf("admin sessions = %#v, want both profiles (2)", all.Sessions)
	}
}

func TestV1AmbientRemoteProfileScopedAttachControlAndAuthorityLoss(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(time.Hour)
	if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	readKey := mintKey(t, st, "ambient-read", "profile-default", store.ScopeReadSessions)
	controlKey := mintKey(t, st, "ambient-control", "profile-default", store.ScopePlaybackControl)

	rec := v1Get(t, srv, "/v1/sessions", readKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var sessions struct {
		Sessions []v1SessionItem `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions: %v", err)
	}
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].SessionID != "session-default" || sessions.Sessions[0].MediaID != "media-alpha" {
		t.Fatalf("sessions = %#v, want attached ambient target session-default/media-alpha", sessions.Sessions)
	}

	rec = v1Post(t, srv, "/v1/playback-control", controlKey, `{"sessionID":"session-default","command":"pause"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/playback-control = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	if body := collectSSE(t, srv, authorizeViewer); !strings.Contains(body, "event: "+store.SessionControlKind) || !strings.Contains(body, "pause") {
		t.Fatalf("same-Profile event stream missing ambient transport command:\n%s", body)
	}

	if err := st.TerminateSession(ctx, "session-default"); err != nil {
		t.Fatalf("TerminateSession: %v", err)
	}
	rec = v1Get(t, srv, "/v1/sessions", readKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions after authority loss = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	sessions.Sessions = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode sessions after authority loss: %v", err)
	}
	if len(sessions.Sessions) != 0 {
		t.Fatalf("sessions after authority loss = %#v, want none", sessions.Sessions)
	}
	rec = v1Post(t, srv, "/v1/playback-control", controlKey, `{"sessionID":"session-default","command":"resume"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST /v1/playback-control after authority loss = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	authorizeViewer(req)
	sse := httptest.NewRecorder()
	srv.ServeHTTP(sse, req)
	if sse.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/events after authority loss = %d, want 401: %s", sse.Code, sse.Body.String())
	}
}

func TestV1PlaybackControlRequiresScopeAndCredential(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	catalogKey := mintKey(t, st, "catalog", "profile-default", store.ScopeReadCatalog)
	body := `{"sessionID":"session-default","command":"pause"}`

	if rec := v1Post(t, srv, "/v1/playback-control", "", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential POST /v1/playback-control = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if rec := v1Post(t, srv, "/v1/playback-control", catalogKey, body); rec.Code != http.StatusForbidden {
		t.Fatalf("catalog key POST /v1/playback-control = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestV1PlaybackControlPublishesTransportCommand(t *testing.T) {
	for _, command := range []string{"pause", "resume", "skip-media", "skip-track"} {
		t.Run(command, func(t *testing.T) {
			srv, st := newTestServerWithStore(t)
			ctx := context.Background()
			future := time.Now().UTC().Add(time.Hour)
			if err := st.RecordStreamToken(ctx, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
				t.Fatalf("RecordStreamToken: %v", err)
			}
			key := mintKey(t, st, "control", "profile-default", store.ScopePlaybackControl)

			rec := v1Post(t, srv, "/v1/playback-control", key, `{"sessionID":"session-default","command":"`+command+`"}`)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("POST /v1/playback-control = %d, want 202: %s", rec.Code, rec.Body.String())
			}
			var resp struct {
				SessionID string `json:"sessionID"`
				Command   string `json:"command"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.SessionID != "session-default" || resp.Command != command {
				t.Fatalf("response = %#v, want session-default/%s", resp, command)
			}
			events := sessionControlEvents(t, st)
			if len(events) != 1 {
				t.Fatalf("session-control events = %d, want 1", len(events))
			}
			if events[0].AggregateID != "session-default" {
				t.Fatalf("event AggregateID = %q, want session-default", events[0].AggregateID)
			}
			var payload store.SessionControlPayload
			if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
				t.Fatalf("decode event payload: %v", err)
			}
			if payload.Command != command || payload.ProfileID != "profile-default" || payload.MediaID != "media-alpha" {
				t.Fatalf("payload = %#v, want command/profile-default/media-alpha", payload)
			}
			entries := sessionControlActivity(t, st)
			if len(entries) != 1 {
				t.Fatalf("session-control activity entries = %d, want 1", len(entries))
			}
			if entries[0].APIKeyID == "" || entries[0].ProfileID != "profile-default" || entries[0].TargetID != "session-default" {
				t.Fatalf("activity entry = %+v, want scoped key attribution for profile-default/session-default", entries[0])
			}
		})
	}
}

func TestV1PlaybackControlAdminScopedKeyCanControlVisibleActiveSessions(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	ctx := context.Background()
	future := time.Now().UTC().Add(time.Hour)
	if err := st.RecordStreamToken(ctx, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false, future); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	key := mintKey(t, st, "control-admin", "", store.ScopePlaybackControl)

	rec := v1Post(t, srv, "/v1/playback-control", key, `{"sessionID":"session-managed","command":"pause"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST /v1/playback-control = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	events := sessionControlEvents(t, st)
	if len(events) != 1 {
		t.Fatalf("session-control events = %d, want 1", len(events))
	}
	var payload store.SessionControlPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload.ProfileID != "profile-managed" || payload.MediaID != "media-alpha" {
		t.Fatalf("payload = %#v, want managed profile/media-alpha", payload)
	}
	entries := sessionControlActivity(t, st)
	if len(entries) != 1 {
		t.Fatalf("session-control activity entries = %d, want 1", len(entries))
	}
	if entries[0].APIKeyID == "" || entries[0].ProfileID != "profile-managed" || entries[0].TargetID != "session-managed" {
		t.Fatalf("activity entry = %+v, want scoped key attribution for managed profile/session-managed", entries[0])
	}
}

func TestV1PlaybackControlRejectsInvalidOrUnavailableTargets(t *testing.T) {
	tests := []struct {
		name string
		seed func(t *testing.T, st *store.Store)
		body string
		want int
	}{
		{
			name: "missing sessionID",
			body: `{"command":"pause"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "unknown command",
			body: `{"sessionID":"session-default","command":"seek"}`,
			want: http.StatusBadRequest,
		},
		{
			name: "inactive session",
			body: `{"sessionID":"session-default","command":"pause"}`,
			want: http.StatusNotFound,
		},
		{
			name: "cross profile session",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				if err := st.RecordStreamToken(context.Background(), "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false, time.Now().UTC().Add(time.Hour)); err != nil {
					t.Fatalf("RecordStreamToken: %v", err)
				}
			},
			body: `{"sessionID":"session-managed","command":"pause"}`,
			want: http.StatusNotFound,
		},
		{
			name: "hidden media session",
			seed: func(t *testing.T, st *store.Store) {
				t.Helper()
				if err := st.RecordStreamToken(context.Background(), "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true, time.Now().UTC().Add(time.Hour)); err != nil {
					t.Fatalf("RecordStreamToken: %v", err)
				}
			},
			body: `{"sessionID":"session-default","command":"pause"}`,
			want: http.StatusNotFound,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, st := newTestServerWithStore(t)
			if tt.seed != nil {
				tt.seed(t, st)
			}
			key := mintKey(t, st, "control", "profile-default", store.ScopePlaybackControl)
			rec := v1Post(t, srv, "/v1/playback-control", key, tt.body)
			if rec.Code != tt.want {
				t.Fatalf("POST /v1/playback-control = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if got := len(sessionControlEvents(t, st)); got != 0 {
				t.Fatalf("session-control events = %d, want 0", got)
			}
			if got := len(sessionControlActivity(t, st)); got != 0 {
				t.Fatalf("session-control activity entries = %d, want 0", got)
			}
		})
	}
}

type fakeBackupV1Store struct {
	*store.Store
	status BackupStatus
	err    error
	called bool
	size   int64
	body   []byte
	class  store.MediaClass
}

type BackupStatus = store.BackupIngestPolicy

type noopBackupEncryptor struct{}

func (noopBackupEncryptor) EncryptFile(_, _, _ string) error { return nil }

func (f *fakeBackupV1Store) BackupIngest(ctx context.Context, req store.BackupIngestRequest) (store.BackupIngestResult, error) {
	f.called = true
	f.size = req.SizeBytes
	f.class = req.Class
	body, readErr := io.ReadAll(req.PlainReader)
	if readErr != nil {
		return store.BackupIngestResult{}, readErr
	}
	f.body = body
	if f.err != nil {
		return store.BackupIngestResult{}, f.err
	}
	return store.BackupIngestResult{
		Media:       store.Media{ID: "media-uploaded", LibraryID: req.LibraryID},
		Fingerprint: "sha256:test",
	}, nil
}

func (f *fakeBackupV1Store) BackupIngestStatus(ctx context.Context, profileID store.ProfileID, libraryID store.LibraryID) (store.BackupIngestPolicy, error) {
	if f.status.ProfileID == "" {
		f.status.ProfileID = profileID
	}
	if f.status.LibraryID == "" {
		f.status.LibraryID = libraryID
	}
	return f.status, nil
}

func TestV1BackupRequiresBackupScopeAndProfileBinding(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	srv.SetV1Store(&fakeBackupV1Store{Store: st})
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")

	catalogKey := mintKey(t, st, "catalog", "profile-default", store.ScopeReadCatalog)
	if rec := v1PostMultipart(t, srv, "/v1/backups/media", catalogKey, body, contentType); rec.Code != http.StatusForbidden {
		t.Fatalf("catalog key POST /v1/backups/media = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	adminBackupKey := mintKey(t, st, "backup-admin", "", store.ScopeBackup)
	body, contentType = backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")
	if rec := v1PostMultipart(t, srv, "/v1/backups/media", adminBackupKey, body, contentType); rec.Code != http.StatusForbidden {
		t.Fatalf("admin-scoped backup key POST /v1/backups/media = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	profileBackupKey := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)
	rec := v1Get(t, srv, "/v1/backups/library-local/status", profileBackupKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile backup key GET status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestV1BackupOwnDataLibraryGateAndSecurePhase(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	fake := &fakeBackupV1Store{Store: st}
	srv.SetV1Store(fake)
	key := mintKey(t, st, "backup-profile", "profile-managed", store.ScopeBackup)

	if rec := v1Get(t, srv, "/v1/backups/library-archive/status", key); rec.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope status = %d, want 404: %s", rec.Code, rec.Body.String())
	}

	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")
	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "secure phase") {
		t.Fatalf("unwired secure phase = %d %s, want 503 secure phase", rec.Code, rec.Body.String())
	}
	if fake.called {
		t.Fatal("backup store called without secure-phase encryptor")
	}

	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	body, contentType = backupMultipartBody(t, map[string]string{"libraryID": "library-archive", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")
	rec = v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope upload = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if fake.called {
		t.Fatal("backup store called for out-of-scope library")
	}
}

func TestV1BackupStreamsUploadBodyIntoIngest(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	fake := &fakeBackupV1Store{Store: st}
	srv.SetV1Store(fake)
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)
	upload := bytes.Repeat([]byte("x"), maxBackupMultipartMemory+1024)
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", upload, "application/octet-stream")

	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)

	if rec.Code != http.StatusCreated {
		t.Fatalf("streaming upload POST = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if fake.size != int64(len(upload)) || !bytes.Equal(fake.body, upload) {
		t.Fatalf("ingest got size=%d body=%d, want %d full bytes", fake.size, len(fake.body), len(upload))
	}
}

func TestV1BackupIngestFailureIsGenericServerError(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	leakedPath := filepath.Join(t.TempDir(), "private", "ciphertext.partial")
	srv.SetV1Store(&fakeBackupV1Store{
		Store: st,
		err:   &os.PathError{Op: "rename", Path: leakedPath, Err: syscall.EIO},
	})
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")

	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("backup ingest filesystem failure = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !strings.Contains(got, "backup ingest failed") || strings.Contains(got, leakedPath) {
		t.Fatalf("backup ingest filesystem failure body = %q, want generic error without path %q", got, leakedPath)
	}
}

func TestV1BackupMalformedMultipartIsBadRequest(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	srv.SetV1Store(&fakeBackupV1Store{Store: st})
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)

	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, []byte("not multipart"), "multipart/form-data; boundary=missing")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed backup multipart = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestV1BackupPausedAndImageDecodeFailuresAreExplicit(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	fake := &fakeBackupV1Store{
		Store:  st,
		status: store.BackupIngestPolicy{Paused: true},
		err:    store.ErrBackupIngestPaused,
	}
	srv.SetV1Store(fake)
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)

	status := v1Get(t, srv, "/v1/backups/library-local/status", key)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"paused":true`) {
		t.Fatalf("backup status = %d %s, want paused true", status.Code, status.Body.String())
	}
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "image"}, "bad.png", []byte("not an image"), "image/png")
	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad image POST /v1/backups/media = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if fake.called {
		t.Fatal("backup store called after hostile image decode failure")
	}

	body, contentType = backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "image"}, "huge.png", hugePNGHeader(t, 10001, 10000), "image/png")
	rec = v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dimensions") {
		t.Fatalf("huge image POST = %d %s, want 400 dimensions", rec.Code, rec.Body.String())
	}

	body, contentType = backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", []byte("media bytes"), "application/octet-stream")
	rec = v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)
	if rec.Code != http.StatusLocked || !strings.Contains(rec.Body.String(), `"paused":true`) {
		t.Fatalf("paused backup POST = %d %s, want 423 paused true", rec.Code, rec.Body.String())
	}
}

func TestV1BackupImageSniffReplaysPrefixIntoIngest(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	fake := &fakeBackupV1Store{Store: st}
	srv.SetV1Store(fake)
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)
	upload := append(hugePNGHeader(t, 32, 24), bytes.Repeat([]byte("x"), 512)...)
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "image"}, "upload.png", upload, "image/png")

	rec := v1PostMultipart(t, srv, "/v1/backups/media", key, body, contentType)

	if rec.Code != http.StatusCreated {
		t.Fatalf("image backup POST = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if fake.class != store.MediaClassImage || !bytes.Equal(fake.body, upload) {
		t.Fatalf("ingest class/body = %q/%d, want image/%d replayed bytes", fake.class, len(fake.body), len(upload))
	}
}

// pacingReader releases body bytes in small delayed chunks so a real HTTP
// client/server round trip over TCP takes measurably longer than a short
// server ReadTimeout, modeling a slow uplink.
type pacingReader struct {
	remaining []byte
	chunk     int
	delay     time.Duration
}

func (p *pacingReader) Read(buf []byte) (int, error) {
	if len(p.remaining) == 0 {
		return 0, io.EOF
	}
	time.Sleep(p.delay)
	n := p.chunk
	if n > len(buf) {
		n = len(buf)
	}
	if n > len(p.remaining) {
		n = len(p.remaining)
	}
	copy(buf, p.remaining[:n])
	p.remaining = p.remaining[n:]
	return n, nil
}

// TestV1BackupUploadOutlivesShortServerReadTimeout pins
// BUGREV0925-CORE-BACKUP-UPLOAD-DEADLINE-1: a backup upload whose body
// arrives slower than the server's global http.Server.ReadTimeout must still
// complete, because v1BackupMedia extends the connection's read deadline
// before it reads the body.
func TestV1BackupUploadOutlivesShortServerReadTimeout(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	srv.SetBackupIngestEncryptor(noopBackupEncryptor{})
	fake := &fakeBackupV1Store{Store: st}
	srv.SetV1Store(fake)
	key := mintKey(t, st, "backup-profile", "profile-default", store.ScopeBackup)
	upload := bytes.Repeat([]byte("x"), 4096)
	body, contentType := backupMultipartBody(t, map[string]string{"libraryID": "library-local", "class": "video"}, "upload.bin", upload, "application/octet-stream")

	ts := httptest.NewUnstartedServer(srv)
	ts.Config.ReadTimeout = 200 * time.Millisecond
	ts.Start()
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/backups/media", &pacingReader{remaining: body, chunk: 200, delay: 40 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("backup upload trickled past the %s server ReadTimeout: %v", ts.Config.ReadTimeout, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("trickled backup upload = %d, want 201: %s", resp.StatusCode, respBody)
	}
	if fake.size != int64(len(upload)) {
		t.Fatalf("ingest size = %d, want %d", fake.size, len(upload))
	}
}

// TestV1BackupUploadDeadlineDoesNotWeakenOtherRoutesTimeout pins acceptance
// criterion 2: only the backup upload route extends its read deadline. The
// scan trigger route, reading its body no faster than the backup upload
// above, must still be aborted by the server's short ReadTimeout.
func TestV1BackupUploadDeadlineDoesNotWeakenOtherRoutesTimeout(t *testing.T) {
	srv, st := newTestServerWithStore(t)
	scanRoot := t.TempDir()
	if err := st.SetLibraryRoots(context.Background(), "library-local", []string{scanRoot}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	key := mintKey(t, st, "ingest", "", store.ScopeIngest)
	// Insignificant JSON whitespace pads the body to the same size class as
	// the backup-upload test above without changing what it decodes to: an
	// empty Root, which the handler resolves to the persisted scanRoot and
	// enqueues — so if the read deadline were (wrongly) extended here too,
	// this would complete and return 202, exactly the failure this test
	// checks for.
	jsonBody := []byte(`{"libraryID":"library-local"` + strings.Repeat(" ", 4096) + `}`)

	ts := httptest.NewUnstartedServer(srv)
	ts.Config.ReadTimeout = 200 * time.Millisecond
	ts.Start()
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/scans", &pacingReader{remaining: jsonBody, chunk: 200, delay: 40 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := ts.Client().Do(req)
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusAccepted {
			t.Fatalf("scan trigger with a body trickled past the %s server ReadTimeout returned 202, want the short timeout to abort a route that does not extend its read deadline", ts.Config.ReadTimeout)
		}
	}
}

func v1PostMultipart(t *testing.T, srv *Server, path, key string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func backupMultipartBody(t *testing.T, fields map[string]string, filename string, fileBody []byte, contentType string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(fileBody)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func hugePNGHeader(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var body bytes.Buffer
	body.Write([]byte("\x89PNG\r\n\x1a\n"))
	writePNGChunk(t, &body, "IHDR", func() []byte {
		data := make([]byte, 13)
		binary.BigEndian.PutUint32(data[0:4], width)
		binary.BigEndian.PutUint32(data[4:8], height)
		data[8] = 8
		data[9] = 2
		return data
	}())
	return body.Bytes()
}

func writePNGChunk(t *testing.T, body *bytes.Buffer, kind string, data []byte) {
	t.Helper()
	if len(kind) != 4 {
		t.Fatalf("png chunk kind %q must be four bytes", kind)
	}
	_ = binary.Write(body, binary.BigEndian, uint32(len(data)))
	body.WriteString(kind)
	body.Write(data)
	sum := crc32.NewIEEE()
	_, _ = sum.Write([]byte(kind))
	_, _ = sum.Write(data)
	_ = binary.Write(body, binary.BigEndian, sum.Sum32())
}

func sessionControlEvents(t *testing.T, st *store.Store) []store.OutboxEvent {
	t.Helper()
	events, err := st.ReadEventOutbox(context.Background(), 0, 1000)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	out := make([]store.OutboxEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == store.SessionControlKind {
			out = append(out, event)
		}
	}
	return out
}

func sessionControlActivity(t *testing.T, st *store.Store) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}})
	if err != nil {
		t.Fatalf("ActivityEntries: %v", err)
	}
	out := make([]store.ActivityEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Action == store.ActivitySessionControl {
			out = append(out, entry)
		}
	}
	return out
}

// TestOpenAPISpecMatchesRoutes pins that the published OpenAPI spec is valid
// JSON and stays consistent with the registered /v1/ routes: every mounted /v1/
// route (method+path) appears as a spec path operation, and every spec /v1/
// operation declares the scope its handler enforces.
func TestOpenAPISpecMatchesRoutes(t *testing.T) {
	specPath := filepath.Join("..", "..", "docs", "api", "openapi.json")
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var spec struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			RequiredScope string `json:"x-required-scope"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if spec.OpenAPI == "" {
		t.Fatalf("spec has no openapi version field")
	}

	// Map mounted /v1/ routes to the spec's path syntax: the server's
	// "{libraryID}" segments are already OpenAPI-style, so a route's pattern is
	// its spec path verbatim.
	srv := newTestServer(t)
	mounted := map[string]bool{} // "GET /v1/libraries" -> true
	for _, route := range srv.Routes() {
		if len(route.Pattern) < 4 || route.Pattern[:4] != "/v1/" {
			continue
		}
		mounted[route.Method+" "+route.Pattern] = true
	}
	if len(mounted) == 0 {
		t.Fatal("no /v1/ routes mounted")
	}

	// Every mounted /v1/ route must be documented.
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			key := upperMethod(method) + " " + path
			documented[key] = true
		}
	}
	for route := range mounted {
		if !documented[route] {
			t.Errorf("mounted /v1/ route %q is not in the OpenAPI spec", route)
		}
	}
	// Every documented /v1/ operation must be a mounted route and declare a scope.
	for path, ops := range spec.Paths {
		for method, op := range ops {
			key := upperMethod(method) + " " + path
			if !mounted[key] {
				t.Errorf("spec documents %q but no such /v1/ route is mounted", key)
			}
			if op.RequiredScope == "" {
				t.Errorf("spec operation %q declares no x-required-scope", key)
			}
		}
	}
}

func upperMethod(m string) string {
	switch m {
	case "get":
		return http.MethodGet
	case "post":
		return http.MethodPost
	case "put":
		return http.MethodPut
	case "delete":
		return http.MethodDelete
	case "patch":
		return http.MethodPatch
	default:
		return m
	}
}
