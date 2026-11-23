package server

import (
	"bytes"
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
	"github.com/beamfall/core/internal/genassets"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestEventStreamPartyStageBindingAndControllerCheck(t *testing.T) {
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
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}

	hostFilter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	partyID, err := st.StartPartySession(ctx, store.PartyStartInput{
		Host: "profile-default", HostName: "Rowan", LibraryIDs: []store.LibraryID{"library-local"}, Filter: hostFilter,
	})
	if err != nil {
		t.Fatal(err)
	}
	joinView, err := st.AddPartyPlayerDisplay(ctx, "profile-default", hostFilter, store.PlayerDisplay{
		DeviceID:          "device-lounge-tv",
		DeviceName:        "Lounge TV",
		PlayerProfileID:   player.ID,
		DisplayName:       "Lounge TV",
		BindingGeneration: 1,
		SessionID:         "session-lounge-tv",
	}, "local")
	if err != nil {
		t.Fatal(err)
	}

	// Screen-bound SSE subscription: another Session gets 404 "screen not found".
	reqOther := httptest.NewRequest(http.MethodGet, "/api/events?screenID="+joinView.Screen.ID, nil)
	authorizeViewer(reqOther)
	recOther := httptest.NewRecorder()
	srv.ServeHTTP(recOther, reqOther)
	if recOther.Code != http.StatusNotFound || !strings.Contains(recOther.Body.String(), "screen not found") {
		t.Fatalf("other-session ?screenID= status=%d body=%s, want 404 screen not found", recOther.Code, recOther.Body.String())
	}

	// Connect compat for player-display capability so display can open /api/events.
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

	// Display receives its party.stage joined event.
	tvBody := collectSSE(t, srv, authTV)
	if !strings.Contains(tvBody, "event: "+store.PartyStageEventKind) || !strings.Contains(tvBody, joinView.Screen.ID) {
		t.Fatalf("display SSE missing party.stage joined event:\n%s", tvBody)
	}
	// Another Session on the server does NOT receive the display's party.stage event.
	viewerBody := collectSSE(t, srv, authorizeViewer)
	if strings.Contains(viewerBody, store.PartyStageEventKind) {
		t.Fatalf("other session SSE leaked party.stage:\n%s", viewerBody)
	}

	// Emit a party change, then remove host from controllers -> changed event must NOT reach display; released event DOES reach display.
	if err := st.AddPartyEntry(ctx, "profile-default", hostFilter, "media-alpha", "", "Avery"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	afterBody := collectSSE(t, srv, authTV)
	if strings.Contains(afterBody, `"action":"changed"`) {
		t.Fatalf("display received party change after host left controllers list:\n%s", afterBody)
	}
	if !strings.Contains(afterBody, `"action":"released"`) {
		t.Fatalf("display missing released event after host left controllers list:\n%s", afterBody)
	}
	_ = partyID
}

func TestPlayerDisplayWalkUpOffPlaysPartyCurrentAndGuestsBlocked(t *testing.T) {
	ctx := context.Background()
	srv, st := newTestServerWithStore(t)
	now := time.Now().UTC()

	player, err := st.CreateProfile(ctx, "Karaoke TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-karaoke-tv", Name: "Karaoke TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("karaoke-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Karaoke TV", PlayerBindingGeneration: 1,
		},
		store.Session{ID: "session-karaoke-tv", DeviceID: "device-karaoke-tv", ProfileID: player.ID, ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	catalog := compatTestCatalog()
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:session-karaoke-tv",
		Surface:      capabilities.SurfaceTVOSViewer,
		BuildID:      compatTestBuildID("tvos-viewer"),
	})
	policy := srv.compat.snapshot()
	policy.Builds = catalog
	srv.SetCompatPolicy(policy, srv.buildSHA, 0)
	authTV := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-karaoke-tv")
		req.Header.Set("X-Beamfall-Device", "device-karaoke-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "karaoke-secret")
	}
	withCap := compatTestContract("tvos-viewer")
	withCap.RequestedCapabilities = []string{string(capabilities.CapabilityPlayerDisplay)}
	if resp := compatHandshakeRecord(t, srv, withCap, authTV); resp.Decision != "compatible" {
		t.Fatalf("compat handshake = %+v", resp)
	}

	hostFilter := store.LibraryFilter{AllLibraries: true, Revealed: true}
	partyID, err := st.StartPartySession(ctx, store.PartyStartInput{
		Host: "profile-default", HostName: "Rowan", LibraryIDs: []store.LibraryID{"library-local"}, Filter: hostFilter,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddPartyEntry(ctx, "profile-default", hostFilter, "media-alpha", "", "Avery"); err != nil {
		t.Fatal(err)
	}
	if err := st.AdvanceParty(ctx, "profile-default", hostFilter, ""); err != nil {
		t.Fatal(err)
	}
	display := store.PlayerDisplay{
		DeviceID: "device-karaoke-tv", DeviceName: "Karaoke TV", PlayerProfileID: player.ID,
		DisplayName: "Karaoke TV", BindingGeneration: 1, SessionID: "session-karaoke-tv",
	}
	join := func() store.PartyScreenJoinView {
		t.Helper()
		screen, err := st.AddPartyPlayerDisplay(ctx, "profile-default", hostFilter, display, "local")
		if err != nil {
			t.Fatal(err)
		}
		return screen
	}
	screen := join()

	// The joined display must get a usable stream for the current party item.
	mediaPath := filepath.Join(t.TempDir(), "alpha.mp4")
	if err := os.WriteFile(mediaPath, []byte("beamfall-media-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", mediaPath); err != nil {
		t.Fatal(err)
	}
	file, err := st.PrimaryMediaFile(ctx, "media-alpha", hostFilter)
	if err != nil {
		t.Fatal(err)
	}
	assets := genassets.NewService(st, nil, t.TempDir())
	clipPath := assets.PreviewClipPath(file.ContentFingerprint)
	if err := os.MkdirAll(filepath.Dir(clipPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(clipPath, []byte("preview bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.playback.SetAssets(testAssetLocator{preview: true})
	srv.media.SetAssets(assets)
	body, err := json.Marshal(playback.InfoRequest{
		MediaID: "media-alpha",
		CapabilityProfile: playback.CapabilityProfile{
			Client: "tvos", Containers: []string{"mp4"}, DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	streamURL := func() string {
		t.Helper()
		pbReq := newJSONRequest(http.MethodPost, "/api/playback-info", bytes.NewReader(body))
		authTV(pbReq)
		pbRec := httptest.NewRecorder()
		srv.ServeHTTP(pbRec, pbReq)
		if pbRec.Code != http.StatusOK {
			t.Fatalf("joined display playback-info = %d: %s", pbRec.Code, pbRec.Body.String())
		}
		var info playback.InfoResponse
		if err := json.Unmarshal(pbRec.Body.Bytes(), &info); err != nil {
			t.Fatal(err)
		}
		if info.Decision == "unavailable" || info.GrantID == "" || len(info.Streams) == 0 || info.Streams[0].URL == "" {
			t.Fatalf("joined display received no playable stream: %+v", info)
		}
		return info.Streams[0].URL
	}

	checkStream := func(url string, wantStatus int) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != wantStatus || (rec.Body.String() == "beamfall-media-bytes") != (wantStatus == http.StatusOK) {
			t.Fatalf("party stream response = %d %q, want status %d", rec.Code, rec.Body.String(), wantStatus)
		}
	}
	previewURL := func() string {
		t.Helper()
		req := newJSONRequest(http.MethodPost, "/api/preview-info", bytes.NewReader(body))
		authTV(req)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var preview playback.PreviewInfoResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || rec.Code != http.StatusOK ||
			preview.Assets == nil || preview.Assets.PreviewClip == "" {
			t.Fatalf("preview-info = %d %+v, %v", rec.Code, preview, err)
		}
		return preview.Assets.PreviewClip
	}
	checkPreview := func(url string, wantStatus int) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != wantStatus || (rec.Body.String() == "preview bytes") != (wantStatus == http.StatusOK) {
			t.Fatalf("preview response = %d %q, want status %d", rec.Code, rec.Body.String(), wantStatus)
		}
	}
	// A display can fetch the party preview before asking for a playback grant.
	partyPreview := previewURL()
	checkPreview(partyPreview, http.StatusOK)
	beforeDrop := streamURL()
	checkStream(beforeDrop, http.StatusOK)
	checkPreview(partyPreview, http.StatusOK)

	// Re-sign the same assets grant in the pre-upgrade format, retaining its
	// valid compatibility decision, subject and expiry.
	const previewBase = "/api/media/assets/"
	const previewFile = "/" + genassets.PreviewClipName
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(0, 0).UTC() })
	tokens.SetCompatDecisionResolver(compatStreamTokenResolver{server: srv})
	grant, err := tokens.UnverifiedGrant(strings.TrimSuffix(strings.TrimPrefix(partyPreview, previewBase), previewFile), streamtoken.TargetMediaAssets)
	if err != nil {
		t.Fatal(err)
	}
	grant.PlayerPreviewBinding = ""
	legacyToken, err := tokens.Issue(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DropPartyScreen(ctx, "profile-default", hostFilter, screen.Screen.ID); err != nil {
		t.Fatal(err)
	}
	checkStream(beforeDrop, http.StatusUnauthorized)
	checkPreview(partyPreview, http.StatusUnauthorized)
	checkPreview(previewBase+legacyToken+previewFile, http.StatusUnauthorized)
	join()
	checkStream(beforeDrop, http.StatusUnauthorized)
	checkPreview(partyPreview, http.StatusUnauthorized)

	beforeStop := streamURL()
	checkStream(beforeStop, http.StatusOK)
	stop, err := store.NewSessionControlPayload(store.SessionCommandStop, store.SessionControlArgs{}, "device-mac", player.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ControlRendererSessionForPlayer(ctx, display.SessionID, 0, "cmd_stop_party_display", stop, nil, now, store.PlayerControlGuard{
		PlayerProfileID: player.ID, CallerProfileID: "profile-default", BindingGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	checkStream(beforeStop, http.StatusUnauthorized)

	// A commanded item also has a preview before playback. Pause preserves
	// its authorization, and removing its originator from controllers ends it.
	var commandPreview string
	for i, cmd := range []string{store.SessionCommandPlayMedia, store.SessionCommandPause} {
		var args store.SessionControlArgs
		if cmd == store.SessionCommandPlayMedia {
			args.MediaID = "media-alpha"
		}
		payload, err := store.NewSessionControlPayload(cmd, args, "device-mac", player.ID, "media-alpha")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.ControlRendererSessionForPlayer(ctx, display.SessionID, int64(i+1), "cmd_party_preview_"+cmd, payload, nil, now, store.PlayerControlGuard{
			PlayerProfileID: player.ID, CallerProfileID: "profile-default", BindingGeneration: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			commandPreview = previewURL()
		}
		checkPreview(commandPreview, http.StatusOK)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	checkPreview(commandPreview, http.StatusUnauthorized)

	// Guest token cannot access /api/events or /api/graphql.
	view, _, _ := st.PartySessionForHost(ctx, "profile-default", hostFilter)
	if _, err := st.JoinParty(ctx, view.Session.JoinCode, "Guest", "guest-hash-1"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/events", ""},
		{http.MethodPost, "/api/graphql", `{"query":"query { partyStage(sessionID: \"` + partyID + `\", screenID: \"s1\") { sessionID } }"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Beamfall-Party-Guest", "bfg_raw-guest-token")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("guest %s %s status = %d, want 401: %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
