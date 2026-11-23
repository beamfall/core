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

	// Alice commands play-media on the Player display.
	playPayload, err := store.NewSessionControlPayload(store.SessionCommandPlayMedia, store.SessionControlArgs{MediaID: "media-alpha"}, "device-alice-v1", player.ID, "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, "sess-tv-v1", 0, "cmd_v1_play_0001", playPayload, nil, now, store.PlayerControlGuard{
		PlayerProfileID: player.ID, CallerProfileID: alice.ID, CallerSessionID: "sess-alice-v1", BindingGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Display requests /api/playback-info -> gets grantID.
	playReq := newJSONRequest(http.MethodPost, "/api/playback-info", strings.NewReader(`{"mediaID":"media-alpha","capabilityProfile":{"client":"tvos","containers":["mp4"],"directCodecs":["h264"]}}`))
	authTV(playReq)
	playRec := httptest.NewRecorder()
	srv.ServeHTTP(playRec, playReq)
	if playRec.Code != http.StatusOK {
		t.Fatalf("playback-info status = %d: %s", playRec.Code, playRec.Body.String())
	}
	var pbResp playback.InfoResponse
	if err := json.Unmarshal(playRec.Body.Bytes(), &pbResp); err != nil {
		t.Fatal(err)
	}
	if pbResp.GrantID == "" {
		t.Fatal("expected non-empty GrantID from /api/playback-info")
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
}
