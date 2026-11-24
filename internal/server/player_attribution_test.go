package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestServerV1PlayerAttributionEndToEnd(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)
	tokens := streamtoken.NewService([]byte("test-token-key"), time.Now)
	tokens.SetRecorder(st)
	srv.playback = playback.NewService(st, tokens, time.Now)
	srv.playback.SetAuth(srv.auth)
	mediaPath := filepath.Join(t.TempDir(), "alpha.mp4")
	if err := os.WriteFile(mediaPath, []byte("beamfall-media-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", mediaPath); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)

	player, err := st.CreateProfile(ctx, "Lounge TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	alice, err := st.CreateProfile(ctx, "Alice", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-tv-v1", Name: "Lounge TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("tv-v1-sec"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Lounge TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "sess-tv-v1", DeviceID: "device-tv-v1", ProfileID: player.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-alice-v1", Name: "Alice Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("alice-v1-sec")},
		store.Session{ID: "sess-alice-v1", DeviceID: "device-alice-v1", ProfileID: alice.ID, ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	catalog := compatTestCatalog()
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:sess-tv-v1",
		Surface:      capabilities.SurfaceTVOSViewer,
		BuildID:      compatTestBuildID("tvos-viewer"),
	})
	policy := srv.compat.snapshot()
	policy.Builds = catalog
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)

	authTV := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "sess-tv-v1")
		req.Header.Set("X-Beamfall-Device", "device-tv-v1")
		req.Header.Set("X-Beamfall-Device-Secret", "tv-v1-sec")
	}
	withCapContract := compatTestContract("tvos-viewer")
	withCapContract.RequestedCapabilities = []string{string(capabilities.CapabilityPlayerDisplay)}
	compatHandshakeRecord(t, srv, withCapContract, authTV)

	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-bob-v1", Name: "Bob Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("bob-v1-sec")},
		store.Session{ID: "sess-bob-v1", DeviceID: "device-bob-v1", ProfileID: "profile-default", ExpiresAt: now.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	playPayload, err := store.NewSessionControlPayload(store.SessionCommandPlayMedia, store.SessionControlArgs{MediaID: "media-alpha"}, "device-alice-v1", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	aliceGuard := store.PlayerControlGuard{
		PlayerProfileID: player.ID, CallerProfileID: alice.ID, CallerSessionID: "sess-alice-v1", BindingGeneration: 1,
	}
	command := func(revision int64, id string, payload store.SessionControlPayload, guard store.PlayerControlGuard) {
		t.Helper()
		if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv-v1", revision, id, payload, nil, now, guard); err != nil {
			t.Fatal(err)
		}
	}
	playbackInfo := func() playback.InfoResponse {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/playback-info", strings.NewReader(`{"mediaID":"media-alpha","nextMediaID":"media-alpha","capabilityProfile":{"client":"tvos","containers":["mp4"],"directCodecs":["h264"]}}`))
		authTV(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("playback-info status = %d: %s", rec.Code, rec.Body.String())
		}
		var resp playback.InfoResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	command(0, "cmd_v1_play_0001", playPayload, aliceGuard)
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, []store.ProfileID{alice.ID}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if resp := playbackInfo(); resp.Decision != "unavailable" || resp.GrantID != "" || len(resp.Streams) != 0 || resp.Next != nil {
			t.Fatalf("playback-info attempt %d after controller removal = %+v", attempt, resp)
		}
	}
	if historical, err := st.IsPlayerCommandedMedia(ctx, "sess-tv-v1", "media-alpha", now); err != nil || !historical {
		t.Fatalf("command history after rejection = %v, %v", historical, err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	command(1, "cmd_v1_play_0002", playPayload, aliceGuard)
	pbResp := playbackInfo()
	if pbResp.GrantID == "" || pbResp.Decision == "unavailable" || len(pbResp.Streams) == 0 {
		t.Fatalf("expected playable grant, got %+v", pbResp)
	}

	// Display reports progress via PUT /v1/media/media-alpha/progress with grantID + sequence.
	progBody := `{"positionSeconds":55,"durationSeconds":120,"eventTimeMS":1000,"grantID":"` + pbResp.GrantID + `","sequence":0}`
	rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", progBody, "", authTV)
	if rec.Code != http.StatusOK {
		t.Fatalf("v1 progress status = %d: %s", rec.Code, rec.Body.String())
	}
	aliceProg, ok, err := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if err != nil || !ok || aliceProg.PositionSeconds != 55 {
		t.Fatalf("Alice progress = %+v ok=%v err=%v, want pos 55", aliceProg, ok, err)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); ok {
		t.Fatal("Player Profile received progress")
	}

	// Duplicate (grantID, sequence: 0) is dropped cleanly.
	dupBody := `{"positionSeconds":90,"durationSeconds":120,"eventTimeMS":2000,"grantID":"` + pbResp.GrantID + `","sequence":0}`
	rec = v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", dupBody, "", authTV)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate v1 progress status = %d: %s", rec.Code, rec.Body.String())
	}
	aliceProg2, _, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if aliceProg2.PositionSeconds != 55 {
		t.Fatalf("duplicate seq 0 overwrote Alice pos to %d, want 55", aliceProg2.PositionSeconds)
	}

	// Negative sequence is rejected 400.
	badBody := `{"positionSeconds":60,"grantID":"` + pbResp.GrantID + `","sequence":-1}`
	rec = v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", badBody, "", authTV)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("negative sequence status = %d, want 400", rec.Code)
	}

	complete := func(grantID string) {
		t.Helper()
		body := `{"positionSeconds":120,"durationSeconds":120,"finished":true,"sequence":1`
		if grantID != "" {
			body += `,"grantID":"` + grantID + `"`
		}
		rec := v1DeviceRequest(t, srv, http.MethodPut, "/v1/media/media-alpha/progress", body+`}`, "", authTV)
		if rec.Code != http.StatusOK {
			t.Fatalf("late completion = %d: %s", rec.Code, rec.Body.String())
		}
	}
	assertUnchanged := func() {
		t.Helper()
		if progress, exists, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || exists {
			t.Fatalf("Bob received late completion: %+v, exists=%v, err=%v", progress, exists, err)
		}
		if progress, _, err := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha"); err != nil || progress.PositionSeconds != 55 || progress.Watched {
			t.Fatalf("Alice's progress changed: %+v, err=%v", progress, err)
		}
	}
	// Bob takes over the same Media. Alice's late completion arrives before
	// Bob's grant exists and must leave his pending command intact.
	bobPayload, err := store.NewSessionControlPayload(store.SessionCommandPlayMedia, store.SessionControlArgs{MediaID: "media-alpha"}, "device-bob-v1", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	bobGuard := store.PlayerControlGuard{
		PlayerProfileID: player.ID, CallerProfileID: "profile-default", CallerSessionID: "sess-bob-v1", BindingGeneration: 1,
	}
	command(2, "cmd_v1_bob_play_1", bobPayload, bobGuard)
	complete(pbResp.GrantID)
	bobPlayback := playbackInfo()
	if bobPlayback.GrantID == "" || bobPlayback.GrantID == pbResp.GrantID || len(bobPlayback.Streams) == 0 {
		t.Fatalf("expected playable replacement grant, got %+v", bobPlayback)
	}
	// A completion without its original grant cannot be credited to Bob.
	complete("")
	assertUnchanged()

	// Report immediately after the server accepts stop, without any display
	// acknowledgement or endPlayback call.
	stop, err := store.NewSessionControlPayload(store.SessionCommandStop, store.SessionControlArgs{}, "device-bob-v1", player.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	command(3, "cmd_v1_bob_stop_1", stop, bobGuard)
	complete(bobPlayback.GrantID)
	assertUnchanged()
	if grant, ok, err := st.PlaybackGrant(ctx, bobPlayback.GrantID); err != nil || !ok || grant.CancelledAt.IsZero() || !grant.EndedAt.IsZero() {
		t.Fatalf("grant after stop and late completion = %+v, exists=%v, err=%v", grant, ok, err)
	}
}
