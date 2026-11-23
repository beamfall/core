package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/store"
)

func TestEventStreamDeliversPlayerSessionControlToControllersWithD6Redaction(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)
	now := time.Now().UTC()

	player, err := st.CreateProfile(ctx, "Lounge TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-lounge-tv", Name: "Lounge TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("lounge-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Lounge TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "session-lounge-tv", DeviceID: "device-lounge-tv", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []store.ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatal(err)
	}
	// Strip library-local from profile-managed so media-alpha is outside its LibraryFilter().
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{}); err != nil {
		t.Fatal(err)
	}

	// Publish a session.control event for the Player Profile naming media-alpha.
	if err := st.AppendSessionControlEvent(ctx, "session-lounge-tv", store.SessionControlPayload{
		Command:         store.SessionCommandPause,
		FromDevice:      "device-mac",
		ProfileID:       player.ID,
		MediaID:         "media-alpha",
		PlayingMediaIDs: []store.MediaID{"media-alpha"},
	}, now); err != nil {
		t.Fatal(err)
	}

	// 1. Adult controller (can see media-alpha) receives the event with mediaID intact.
	adultBody := collectSSE(t, srv, authorizeViewer)
	if !strings.Contains(adultBody, "event: "+store.SessionControlKind) || !strings.Contains(adultBody, "media-alpha") {
		t.Fatalf("adult controller SSE missing event or mediaID:\n%s", adultBody)
	}

	// 2. Managed controller (cannot see media-alpha) receives the event WITHOUT mediaID / playingMediaIDs and with privateItem=true.
	managedBody := collectSSE(t, srv, authorizeManaged)
	if !strings.Contains(managedBody, "event: "+store.SessionControlKind) {
		t.Fatalf("managed controller SSE missing session.control event:\n%s", managedBody)
	}
	if strings.Contains(managedBody, "media-alpha") {
		t.Fatalf("managed controller SSE leaked hidden mediaID:\n%s", managedBody)
	}
	if !strings.Contains(managedBody, `"privateItem":true`) {
		t.Fatalf("managed controller SSE missing privateItem=true:\n%s", managedBody)
	}

	// 3. Removing profile-managed from controllers list stops delivery on the next poll.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	removedBody := collectSSE(t, srv, authorizeManaged)
	if strings.Contains(removedBody, store.SessionControlKind) {
		t.Fatalf("removed controller still received session.control:\n%s", removedBody)
	}

	// 4. Display itself receives the event with media-alpha intact.
	catalog := compatTestCatalog()
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:session-lounge-tv",
		Surface:      capabilities.SurfaceTVOSViewer,
		BuildID:      compatTestBuildID("tvos-viewer"),
	})
	policy := srv.compat.snapshot()
	policy.Builds = catalog
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)
	authTV := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-lounge-tv")
		req.Header.Set("X-Beamfall-Device", "device-lounge-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "lounge-secret")
	}
	withCap := compatTestContract("tvos-viewer")
	withCap.RequestedCapabilities = []string{string(capabilities.CapabilityPlayerDisplay)}
	if resp := compatHandshakeRecord(t, srv, withCap, authTV); resp.Decision != "compatible" {
		t.Fatalf("compat handshake = %+v", resp)
	}

	displayBody := collectSSE(t, srv, authTV)
	if !strings.Contains(displayBody, "event: "+store.SessionControlKind) || !strings.Contains(displayBody, "media-alpha") {
		t.Fatalf("display SSE missing event or mediaID:\n%s", displayBody)
	}
}

func TestEventStreamKeepaliveTouchesPlayerDisplayPresenceAndLastSeen(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)
	srv.eventStreams.keepalive = 40 * time.Millisecond
	now := time.Now().UTC()

	player, err := st.CreateProfile(ctx, "Den TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-den-tv", Name: "Den TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("den-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Den TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "session-den-tv", DeviceID: "device-den-tv", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	catalog := compatTestCatalog()
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:session-den-tv",
		Surface:      capabilities.SurfaceTVOSViewer,
		BuildID:      compatTestBuildID("tvos-viewer"),
	})
	policy := srv.compat.snapshot()
	policy.Builds = catalog
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)
	authTV := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-den-tv")
		req.Header.Set("X-Beamfall-Device", "device-den-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "den-secret")
	}
	withCap := compatTestContract("tvos-viewer")
	withCap.RequestedCapabilities = []string{string(capabilities.CapabilityPlayerDisplay)}
	if resp := compatHandshakeRecord(t, srv, withCap, authTV); resp.Decision != "compatible" {
		t.Fatalf("compat handshake = %+v", resp)
	}

	// Open SSE stream long enough for at least one keepalive frame, while checking
	// playerDisplays reports "ready" during the stream.
	var duringPresence string
	serveStreamForWithAuth(t, srv, authTV, 120*time.Millisecond, func() {
		deadline := time.Now().Add(100 * time.Millisecond)
		for time.Now().Before(deadline) {
			gReq := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"query { playerDisplays { displayName presence } }"}`))
			authorizeViewer(gReq)
			gRec := httptest.NewRecorder()
			srv.ServeHTTP(gRec, gReq)
			var body struct {
				Data struct {
					PlayerDisplays []struct {
						DisplayName string `json:"displayName"`
						Presence    string `json:"presence"`
					} `json:"playerDisplays"`
				} `json:"data"`
			}
			if err := json.Unmarshal(gRec.Body.Bytes(), &body); err == nil && len(body.Data.PlayerDisplays) == 1 {
				duringPresence = body.Data.PlayerDisplays[0].Presence
				if duringPresence == "ready" {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	if duringPresence != "ready" {
		t.Fatalf("presence during live SSE stream = %q, want ready", duringPresence)
	}

	// After stream closes, presence becomes offline and last_seen_at_ms was persisted.
	gReq := newJSONRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"query { playerDisplays { presence lastSeenAt } }"}`))
	authorizeViewer(gReq)
	gRec := httptest.NewRecorder()
	srv.ServeHTTP(gRec, gReq)
	var after struct {
		Data struct {
			PlayerDisplays []struct {
				Presence   string `json:"presence"`
				LastSeenAt string `json:"lastSeenAt"`
			} `json:"playerDisplays"`
		} `json:"data"`
	}
	if err := json.Unmarshal(gRec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Data.PlayerDisplays) != 1 || after.Data.PlayerDisplays[0].Presence != "offline" || after.Data.PlayerDisplays[0].LastSeenAt == "" {
		t.Fatalf("after stream closed = %+v, want offline with lastSeenAt", after.Data.PlayerDisplays)
	}
}

func serveStreamForWithAuth(t *testing.T, srv *Server, authorize func(*http.Request), d time.Duration, whileStreaming func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	authorize(req)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, req)
		close(done)
	}()
	if whileStreaming != nil {
		whileStreaming()
	}
	<-done
}

// ADR-0240 D3/D4: /v1/playback-control carries no controller identity or
// revision, so a player display is unreachable through it for any key, with or
// without a Profile binding, and nothing reaches the display's event stream.
func TestV1PlaybackControlRefusesPlayerTargets(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)
	now := time.Now().UTC()
	player, err := st.CreateProfile(ctx, "Lounge TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-lounge-tv", Name: "Lounge TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("lounge-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Lounge TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "session-lounge-tv", DeviceID: "device-lounge-tv", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordStreamToken(ctx, "session-lounge-tv", player.ID, "device-lounge-tv", "media-direct", "media-alpha", false, now.Add(time.Hour)); err != nil {
		t.Fatalf("RecordStreamToken: %v", err)
	}
	for _, key := range []string{
		mintKey(t, st, "unbound-control", "", store.ScopePlaybackControl),
		mintKey(t, st, "player-control", player.ID, store.ScopePlaybackControl),
	} {
		rec := v1Post(t, srv, "/v1/playback-control", key, `{"sessionID":"session-lounge-tv","command":"pause"}`)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "session not found") {
			t.Fatalf("POST /v1/playback-control to player = %d %s, want 404 session not found", rec.Code, rec.Body.String())
		}
	}
	if body := collectSSE(t, srv, authorizeViewer); strings.Contains(body, store.SessionControlKind) {
		t.Fatalf("refused player command reached the event stream:\n%s", body)
	}
}
