//go:build io_e2e

package ioe2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/beam"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/manifest"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/pairing"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// scenario:first-run-add-library-media-appears
// io:first-run-add-library-media-appears
func TestP1_first_run_add_library_media_appears(t *testing.T) {
	st := storetest.Open(t)
	srv, pairingService := newJourneyServer(t, st, nil)
	pairingService.SetBootstrapToken("setup-token")

	setup := newJourneyJSONRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"token":"setup-token","deviceName":"Console","profileName":"Owner"}`))
	setup.Header.Set(auth.CSRFHeader, "csrf")
	setupRec := httptest.NewRecorder()
	srv.ServeHTTP(setupRec, setup)
	if setupRec.Code != http.StatusCreated {
		t.Fatalf("setup status = %d: %s", setupRec.Code, setupRec.Body.String())
	}
	cookies := setupRec.Result().Cookies()
	sessionID := cookieValue(t, cookies, auth.SessionCookie)
	if err := st.SetSessionAdminUntil(context.Background(), store.SessionID(sessionID), time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	create := postJourneyServerGraph(t, srv, cookies,
		`mutation($title:String!,$roots:[String!]){ createLibrary(title:$title, roots:$roots) { id title roots } }`,
		map[string]any{"title": "First Run Library", "roots": []string{root}})
	create.mustHaveNoErrors(t)
	var created graph.LibraryInfo
	if err := json.Unmarshal(create.Data["createLibrary"], &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "First Run Library" || len(created.Roots) != 1 || created.Roots[0] != root {
		t.Fatalf("created library = %#v", created)
	}

	writeSettledJourneyFile(t, root, "first-run.mp4", []byte("first run media"))
	if result, err := scan.Scan(context.Background(), st, nil, nil, nil, nil, nil, scan.Request{LibraryID: store.LibraryID(created.ID), Root: root}); err != nil {
		t.Fatal(err)
	} else if result.MediaImported != 1 {
		t.Fatalf("scan imported %d media, want 1: %+v", result.MediaImported, result)
	}
	items := postJourneyServerGraph(t, srv, cookies,
		`query($kind:String!){ sourceItems(kind:$kind) { id title } }`,
		map[string]any{"kind": "all-items"})
	items.mustHaveNoErrors(t)
	if !strings.Contains(items.body, "first-run") && !strings.Contains(items.body, "first-run.mp4") {
		t.Fatalf("scanned media did not appear: %s", items.body)
	}
}

// scenario:resume-scrobble-round-trip
// io:resume-scrobble-round-trip
func TestP1_resume_scrobble_round_trip(t *testing.T) {
	st := storetest.Open(t)
	h := basicJourneyHandler(st)
	secrets := &journeySecretStore{values: map[string]string{}}
	obs := &journeyWatchedObserver{}
	h.SetSubscriptionSecrets(secrets)
	h.SetWatchedObserver(obs)
	if err := st.SetScrobbleLibraryEligibility(context.Background(), "library-local", true); err != nil {
		t.Fatal(err)
	}
	mediaID := importJourneyMedia(t, st, "library-local", "/journey/p1-resume.mp4", "P1 Resume", "p1-resume-os")

	const scrobbleSecret = "oauth-refresh-secret-p1"
	link := postJourneyGraph(t, h, authorizeJourneyViewer,
		`mutation($service:String!,$credential:String!){ linkScrobble(service:$service, credential:$credential, accountLabel:"p1") { service accountLabel linked } }`,
		map[string]any{"service": "trakt", "credential": scrobbleSecret})
	link.mustHaveNoErrors(t)
	if strings.Contains(link.body, scrobbleSecret) {
		t.Fatalf("linkScrobble echoed secret: %s", link.body)
	}
	if got := secrets.values["scrobble-profile-default-trakt"]; got != scrobbleSecret {
		t.Fatalf("secret store = %q, want credential", got)
	}

	resume := postJourneyGraph(t, h, authorizeJourneyViewer,
		`mutation($mediaID:String!,$positionSeconds:Int!,$durationSeconds:Int!){ reportPlayback(mediaID:$mediaID, positionSeconds:$positionSeconds, durationSeconds:$durationSeconds) { mediaID positionSeconds watched } }`,
		map[string]any{"mediaID": string(mediaID), "positionSeconds": 75, "durationSeconds": 120})
	resume.mustHaveNoErrors(t)
	if !strings.Contains(resume.body, `"positionSeconds":75`) || strings.Contains(resume.body, `"watched":true`) {
		t.Fatalf("resume report = %s", resume.body)
	}
	shelf := postJourneyGraph(t, h, authorizeJourneyViewer,
		`query($kind:String!){ sourceItems(kind:$kind) { id title } }`,
		map[string]any{"kind": "continue-watching"})
	shelf.mustHaveNoErrors(t)
	if !strings.Contains(shelf.body, string(mediaID)) {
		t.Fatalf("resume item missing from continue watching: %s", shelf.body)
	}

	finish := postJourneyGraph(t, h, authorizeJourneyViewer,
		`mutation($mediaID:String!,$positionSeconds:Int!,$durationSeconds:Int!,$finished:Boolean!){ reportPlayback(mediaID:$mediaID, positionSeconds:$positionSeconds, durationSeconds:$durationSeconds, finished:$finished) { mediaID watched } }`,
		map[string]any{"mediaID": string(mediaID), "positionSeconds": 120, "durationSeconds": 120, "finished": true})
	finish.mustHaveNoErrors(t)
	if len(obs.transitions) != 1 || obs.transitions[0].MediaID != mediaID || obs.transitions[0].ProfileID != "profile-default" {
		t.Fatalf("watched transition = %+v", obs.transitions)
	}
	links := postJourneyGraph(t, h, authorizeJourneyViewer,
		`query { scrobbleLinks { service accountLabel linked } }`,
		nil)
	links.mustHaveNoErrors(t)
	if !strings.Contains(links.body, `"service":"trakt"`) || strings.Contains(links.body, scrobbleSecret) {
		t.Fatalf("scrobble link round-trip leaked or lost state: %s", links.body)
	}
}

// scenario:secret-leak-to-logs
// io:secret-leak-to-logs
func TestP1_secret_leak_to_logs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	st := storetest.Open(t)
	srv, pairingService := newJourneyServer(t, st, nil)
	if err := st.EnrollDevice(context.Background(),
		store.Device{ID: "device-expired-p1", Name: "Expired", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("refresh-secret-p1")},
		store.Session{ID: "session-expired-p1", DeviceID: "device-expired-p1", ProfileID: "profile-default", ExpiresAt: time.Unix(-1, 0).UTC()},
	); err != nil {
		t.Fatal(err)
	}
	pairingService.SetBootstrapToken("pair-expected-p1")

	cases := []struct {
		name      string
		secret    string
		request   *http.Request
		wantState int
	}{
		{
			name:      "invalid oauth token",
			secret:    "oauth-token-secret-p1",
			request:   bearerJourneyRequest("oauth-token-secret-p1"),
			wantState: http.StatusUnauthorized,
		},
		{
			name:      "expired refresh credential",
			secret:    "refresh-secret-p1",
			request:   expiredJourneyRequest("refresh-secret-p1"),
			wantState: http.StatusUnauthorized,
		},
		{
			name:      "pair challenge failure",
			secret:    "pair-presented-secret-p1",
			request:   setupJourneyRequest("pair-presented-secret-p1"),
			wantState: http.StatusUnauthorized,
		},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, tc.request)
		if rec.Code != tc.wantState {
			t.Fatalf("%s status = %d, want %d: %s", tc.name, rec.Code, tc.wantState, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), tc.secret) {
			t.Fatalf("%s response leaked secret: %s", tc.name, rec.Body.String())
		}
	}
	for _, secret := range []string{"oauth-token-secret-p1", "refresh-secret-p1", "pair-presented-secret-p1"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("logs leaked %q: %s", secret, logs.String())
		}
	}
}

type journeySecretStore struct {
	values map[string]string
}

func (s *journeySecretStore) SetSecret(ref, value string) error {
	if strings.TrimSpace(value) == "" {
		delete(s.values, ref)
		return nil
	}
	s.values[ref] = value
	return nil
}

type journeyWatchedObserver struct {
	transitions []graph.WatchedTransition
}

func (o *journeyWatchedObserver) Watched(t graph.WatchedTransition) {
	o.transitions = append(o.transitions, t)
}

func newJourneyServer(t *testing.T, st *store.Store, handler *graph.Handler) (*server.Server, *pairing.Service) {
	t.Helper()
	clock := func() time.Time { return time.Unix(0, 0).UTC() }
	authService := auth.NewServiceWithClock(st, clock)
	manifests := manifest.NewService(st, clock, nil)
	tokens := streamtoken.NewService([]byte("journey-token-key"), clock)
	tokens.SetRecorder(st)
	pairingService := pairing.NewService(st, clock)
	if handler == nil {
		handler = graph.NewHandler(manifests, authService, st, nil)
	}
	return server.New(
		manifests,
		playback.NewService(st, tokens, clock),
		beam.NewService(st, authService, clock),
		pairingService,
		handler,
		mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)),
		authService,
		st,
	), pairingService
}

func postJourneyServerGraph(t *testing.T, srv http.Handler, cookies []*http.Cookie, query string, variables map[string]any) journeyGraphResponse {
	t.Helper()
	handshakeJourneyServer(t, srv, cookies)
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	req := newJourneyJSONRequest(http.MethodPost, "/api/graphql", bytes.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "csrf")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("graphql status = %d: %s", rec.Code, rec.Body.String())
	}
	var out journeyGraphResponse
	out.body = rec.Body.String()
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode graphql response: %v\n%s", err, out.body)
	}
	return out
}

func handshakeJourneyServer(t *testing.T, srv http.Handler, cookies []*http.Cookie) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"surface":                   "core-web-ui",
		"appVersion":                "ioe2e",
		"buildID":                   "embedded-core-web-ui-v1",
		"coreContractRevRange":      [2]int{1, 1},
		"renderingContractRevRange": [2]int{1, 1},
		"renderingStamps":           []string{"core-web-ui-v1"},
		"fixedVocabularies":         []string{},
		"requestedCapabilities":     []string{},
		"developerAPIMajor":         0,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := newJourneyJSONRequest(http.MethodPost, "/api/compat/handshake", bytes.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "csrf")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("compat handshake status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Decision   string `json:"decision"`
		DecisionID string `json:"decisionID"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode compat handshake: %v", err)
	}
	if response.Decision != "compatible" || response.DecisionID == "" {
		t.Fatalf("compat handshake = %#v, want compatible decision with id", response)
	}
}

func newJourneyJSONRequest(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Content-Type", "application/json")
	return req
}

func cookieValue(t *testing.T, cookies []*http.Cookie, name string) string {
	t.Helper()
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("missing cookie %s", name)
	return ""
}

func bearerJourneyRequest(secret string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	return req
}

func expiredJourneyRequest(secret string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/manifest", nil)
	req.Header.Set("X-Beamfall-Session", "session-expired-p1")
	req.Header.Set("X-Beamfall-Device", "device-expired-p1")
	req.Header.Set("X-Beamfall-Device-Secret", secret)
	return req
}

func setupJourneyRequest(secret string) *http.Request {
	req := newJourneyJSONRequest(http.MethodPost, "/api/setup", strings.NewReader(`{"token":"`+secret+`","deviceName":"Console"}`))
	req.Header.Set(auth.CSRFHeader, "csrf")
	return req
}
