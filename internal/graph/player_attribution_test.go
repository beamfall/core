package graph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

type playbackObserverAdapter struct {
	pb *playback.Service
}

func (a playbackObserverAdapter) PlayObservation(sessionID store.SessionID, mediaID store.MediaID) (PlayObservation, bool) {
	obs, ok := a.pb.PlayObservation(sessionID, mediaID)
	if !ok {
		return PlayObservation{}, false
	}
	return PlayObservation{Method: obs.Method, Reason: obs.Reason, Bitrate: obs.Bitrate}, true
}

func setupPlayerAttributionHarness(t *testing.T) (
	st *store.Store,
	h *Handler,
	pb *playback.Service,
	tokens *streamtoken.Service,
	clock *time.Time,
	player store.Profile,
	displaySess store.SessionID,
	alice store.Profile,
	authAlice func(*http.Request),
	bob store.Profile,
	authBob func(*http.Request),
	authDisplay func(*http.Request),
) {
	t.Helper()
	ctx := context.Background()
	st = storetest.Open(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	clock = &now
	h = newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return *clock }

	tokens = streamtoken.NewService([]byte("12345678901234567890123456789012"), func() time.Time { return *clock })
	tokens.SetRecorder(st)
	tokens.SetPlaybackGrantResolver(st)
	h.tokens = tokens
	pb = playback.NewService(st, tokens, func() time.Time { return *clock })
	pb.SetAuth(h.auth)
	h.playbackObserver = playbackObserverAdapter{pb}

	// Back media-alpha with a real file so playbackInfo issues a real stream grant.
	mediaPath := t.TempDir() + "/alpha.mp4"
	if err := os.WriteFile(mediaPath, []byte("alpha-media-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPrimaryMediaFile(ctx, "media-alpha", mediaPath); err != nil {
		t.Fatal(err)
	}

	all := store.LibraryFilter{Revealed: true, AllLibraries: true}
	var err error
	player, err = st.CreateProfile(ctx, "Karaoke Room", store.ProfilePlayer, all)
	if err != nil {
		t.Fatal(err)
	}
	displaySess = "session-karaoke-tv"
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-karaoke-tv", Name: "Apple TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("tv-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Karaoke room TV", PlayerBindingGeneration: 1,
		},
		store.Session{
			ID: displaySess, DeviceID: "device-karaoke-tv", ProfileID: player.ID,
			ExpiresAt: clock.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatal(err)
	}
	h.EventStreamOpened(displaySess)

	alice, err = st.CreateProfile(ctx, "Alice", store.ProfileAdult, all)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-alice", Name: "Alice Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("alice-sec")},
		store.Session{ID: "session-alice", DeviceID: "device-alice", ProfileID: alice.ID, ExpiresAt: clock.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authAlice = func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-alice")
		req.Header.Set("X-Beamfall-Device", "device-alice")
		req.Header.Set("X-Beamfall-Device-Secret", "alice-sec")
	}

	bob, err = st.CreateProfile(ctx, "Bob", store.ProfileAdult, all)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-bob", Name: "Bob Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("bob-sec")},
		store.Session{ID: "session-bob", DeviceID: "device-bob", ProfileID: bob.ID, ExpiresAt: clock.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authBob = func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-bob")
		req.Header.Set("X-Beamfall-Device", "device-bob")
		req.Header.Set("X-Beamfall-Device-Secret", "bob-sec")
	}

	authDisplay = func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", string(displaySess))
		req.Header.Set("X-Beamfall-Device", "device-karaoke-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "tv-secret")
		*req = *req.WithContext(auth.WithClientCapabilities(req.Context(), []string{string(capabilities.CapabilityPlayerDisplay)}))
	}
	return
}

func displayPlaybackInfo(t *testing.T, st *store.Store, pb *playback.Service, clock time.Time, displaySess store.SessionID, mediaID string) playback.InfoResponse {
	t.Helper()
	ctx := context.Background()
	authSvc := auth.NewServiceWithClock(st, func() time.Time { return clock })
	scope, err := authSvc.Scope(ctx, displaySess)
	if err != nil {
		t.Fatalf("Scope(%s): %v", displaySess, err)
	}
	resp, err := pb.Info(ctx, scope, playback.InfoRequest{
		MediaID: mediaID,
		CapabilityProfile: playback.CapabilityProfile{
			Client:       "tvos",
			Containers:   []string{"mp4"},
			DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatalf("pb.Info(%s): %v", mediaID, err)
	}
	return resp
}

func reportPlaybackVars(t *testing.T, h *Handler, vars map[string]any, authFn func(*http.Request)) (PlaybackReport, []graphError) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"query": `mutation R($mediaID: String!, $positionSeconds: Int, $durationSeconds: Int, $finished: Boolean, $ambient: Boolean, $eventTimeMS: Int, $paused: Boolean, $playbackID: String, $playedMS: Int, $grantID: String, $sequence: Int) {
			reportPlayback(mediaID: $mediaID, positionSeconds: $positionSeconds, durationSeconds: $durationSeconds, finished: $finished, ambient: $ambient, eventTimeMS: $eventTimeMS, paused: $paused, playbackID: $playbackID, playedMS: $playedMS, grantID: $grantID, sequence: $sequence) {
				mediaID positionSeconds watched
			}
		}`,
		"variables": vars,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(string(body)))
	authFn(req)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var parsed struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphError               `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode reportPlayback: %v: %s", err, rec.Body.String())
	}
	var report PlaybackReport
	if raw, ok := parsed.Data["reportPlayback"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
	}
	return report, parsed.Errors
}

func TestPlayerAttributionFullFlowIdempotencyAndConsent(t *testing.T) {
	ctx := context.Background()
	st, h, pb, tokens, clock, player, displaySess, alice, authAlice, bob, authBob, authDisplay := setupPlayerAttributionHarness(t)
	obs := &fakeWatchedObserver{}
	h.SetWatchedObserver(obs)

	// Give Alice a scrobble link (and verify Bob's different link is never touched).
	if err := st.UpsertScrobbleLink(ctx, store.ScrobbleLink{
		ProfileID: alice.ID, Service: "trakt", SecretRef: "ref-alice", AccountLabel: "alice", LinkedAt: *clock,
	}); err != nil {
		t.Fatal(err)
	}
	// Seed Alice's resume position at 35s.
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: alice.ID, MediaID: "media-alpha", PositionSeconds: 35, DurationSeconds: 120,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	// 1. Alice sends play-media (omitting positionMs) to the Player display.
	_, errs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_alice_play_0001",
		"command": "play-media", "mediaID": "media-alpha",
	}, authAlice)
	if len(errs) != 0 {
		t.Fatalf("Alice play-media errors = %#v", errs)
	}

	// 2. Display requests playbackInfo -> gets grantID, stream token with GrantID, and Alice's 35s resume position.
	info := displayPlaybackInfo(t, st, pb, *clock, displaySess, "media-alpha")
	if info.Decision == playback.DecisionUnavailable || info.GrantID == "" {
		t.Fatalf("display playbackInfo = %+v, want playable with GrantID", info)
	}
	if info.Timeline.PositionSeconds != 35 {
		t.Fatalf("display timeline.PositionSeconds = %d, want 35 (Alice's resume)", info.Timeline.PositionSeconds)
	}
	tokGrant, err := tokens.UnverifiedGrant(info.Streams[0].Token, streamtoken.TargetMediaDirect)
	if err != nil || tokGrant.GrantID != info.GrantID {
		t.Fatalf("token GrantID = %q err=%v, want %q", tokGrant.GrantID, err, info.GrantID)
	}

	// 3. Bob sends pause while playing -> originator must stay Alice!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 1, "commandID": "cmd_bob_pause_0001",
		"command": "pause",
	}, authBob)
	if len(errs) != 0 {
		t.Fatalf("Bob pause errors = %#v", errs)
	}

	// 4. Display reports progress (seq 0) with a spoofed profileID variable -> ignored; lands on Alice, never Player Profile or Bob!
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 60, "durationSeconds": 120,
		"eventTimeMS": 1000, "playbackID": "play-disp-1", "playedMS": 25000,
		"grantID": info.GrantID, "sequence": 0, "profileID": string(bob.ID),
	}, authDisplay)
	if len(errs) != 0 {
		t.Fatalf("reportPlayback seq 0 errors = %#v", errs)
	}
	aliceProg, ok, err := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if err != nil || !ok || aliceProg.PositionSeconds != 60 || aliceProg.Watched {
		t.Fatalf("Alice progress after seq 0 = %+v ok=%v err=%v, want pos=60", aliceProg, ok, err)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); ok {
		t.Fatal("Player Profile received a playback_progress row")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, bob.ID, "media-alpha"); ok {
		t.Fatal("Bob received a playback_progress row")
	}

	// 5. Duplicate (grantID, seq 0) is a no-op (does not re-credit playedMS or overwrite newer progress).
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 75, "durationSeconds": 120,
		"eventTimeMS": 2000, "playbackID": "play-disp-1", "playedMS": 40000,
		"grantID": info.GrantID, "sequence": 0,
	}, authDisplay)
	if len(errs) != 0 {
		t.Fatalf("duplicate seq 0 errors = %#v", errs)
	}
	aliceProgAfterDup, _, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if aliceProgAfterDup.PositionSeconds != 60 {
		t.Fatalf("duplicate seq 0 mutated Alice progress to %d, want 60", aliceProgAfterDup.PositionSeconds)
	}

	// 6. Completion report (seq 1, finished=true) marks Alice watched and fires scrobble observer ONLY for Alice.
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 120, "durationSeconds": 120,
		"finished": true, "eventTimeMS": 3000, "playbackID": "play-disp-1", "playedMS": 85000,
		"grantID": info.GrantID, "sequence": 1,
	}, authDisplay)
	if len(errs) != 0 {
		t.Fatalf("finish seq 1 errors = %#v", errs)
	}
	aliceDone, _, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if !aliceDone.Watched || aliceDone.PlayCount != 1 {
		t.Fatalf("Alice after finish = %+v, want watched=true playCount=1", aliceDone)
	}
	if obs.count() != 1 || obs.got[0].ProfileID != alice.ID {
		t.Fatalf("watchedObserver = %+v, want 1 transition for Alice (%s)", obs.got, alice.ID)
	}

	// 7. Report on ended grant (seq 2) is dropped (no double playCount or scrobble).
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 120, "durationSeconds": 120,
		"finished": true, "eventTimeMS": 4000, "grantID": info.GrantID, "sequence": 2,
	}, authDisplay)
	if len(errs) != 0 {
		t.Fatalf("post-finish seq 2 errors = %#v", errs)
	}
	aliceAfterEnded, _, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha")
	if aliceAfterEnded.PlayCount != 1 || obs.count() != 1 {
		t.Fatalf("post-finish mutated playCount=%d obs=%d", aliceAfterEnded.PlayCount, obs.count())
	}
	// Player Profile still has zero My Data engagement!
	if _, ok, _ := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); ok {
		t.Fatal("Player Profile has playback_progress after finish")
	}
}

func TestPlayerAttributionOldGenerationDeletedOriginatorWalkUpAndAmbient(t *testing.T) {
	ctx := context.Background()
	st, h, pb, _, clock, player, displaySess, alice, authAlice, bob, authBob, authDisplay := setupPlayerAttributionHarness(t)

	// 1. Rebinding increments player_binding_generation and drops old-generation reports.
	_, errs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_gen1_play_0001",
		"command": "play-media", "mediaID": "media-alpha",
	}, authAlice)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	oldInfo := displayPlaybackInfo(t, st, pb, *clock, displaySess, "media-alpha")
	if oldInfo.GrantID == "" {
		t.Fatal("expected grantID")
	}
	if _, err := st.SetDevicePlayerBinding(ctx, "device-karaoke-tv", player.ID, "Karaoke TV Gen2"); err != nil {
		t.Fatal(err)
	}
	displaySess2 := store.SessionID("session-karaoke-tv-2")
	if _, err := st.RenewDeviceSession(ctx, "device-karaoke-tv", displaySess2, clock.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.EventStreamOpened(displaySess2)
	authDisplay2 := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", string(displaySess2))
		req.Header.Set("X-Beamfall-Device", "device-karaoke-tv")
		req.Header.Set("X-Beamfall-Device-Secret", "tv-secret")
		*req = *req.WithContext(auth.WithClientCapabilities(req.Context(), []string{string(capabilities.CapabilityPlayerDisplay)}))
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 50, "durationSeconds": 120,
		"grantID": oldInfo.GrantID, "sequence": 0,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatalf("old generation report errors = %#v", errs)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha"); ok {
		t.Fatal("report on old generation wrote progress for Alice")
	}

	// 2. Deleted originator: playing item finishes on its grant and records nothing;
	//    pending entry for deleted originator is skipped when becoming current.
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess2), "expectedRevision": 0, "commandID": "cmd_bob_playing_0001",
		"command": "play-media", "mediaID": "media-alpha",
	}, authBob)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	playingInfo := displayPlaybackInfo(t, st, pb, *clock, displaySess2, "media-alpha")
	if playingInfo.GrantID == "" || playingInfo.Decision == playback.DecisionUnavailable {
		t.Fatalf("playingInfo = %+v", playingInfo)
	}
	if err := st.DeleteProfile(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 120, "durationSeconds": 120,
		"finished": true, "grantID": playingInfo.GrantID, "sequence": 0,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatalf("deleted originator finish report errors = %#v", errs)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, bob.ID, "media-alpha"); ok {
		t.Fatal("deleted originator received progress")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); ok {
		t.Fatal("player profile received progress")
	}

	// Pending entry for deleted originator (charlie) is skipped at playbackInfo.
	charlie, err := st.CreateProfile(ctx, "Charlie", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-charlie", Name: "Charlie Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("c-sec")},
		store.Session{ID: "session-charlie", DeviceID: "device-charlie", ProfileID: charlie.ID, ExpiresAt: clock.Add(24 * time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authCharlie := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-charlie")
		req.Header.Set("X-Beamfall-Device", "device-charlie")
		req.Header.Set("X-Beamfall-Device-Secret", "c-sec")
	}
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess2), "expectedRevision": 1, "commandID": "cmd_charlie_pending1",
		"command": "play-media", "mediaID": "media-alpha",
	}, authCharlie)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if err := st.DeleteProfile(ctx, charlie.ID); err != nil {
		t.Fatal(err)
	}
	pendingResp := displayPlaybackInfo(t, st, pb, *clock, displaySess2, "media-alpha")
	if pendingResp.Decision != playback.DecisionUnavailable || pendingResp.GrantID != "" {
		t.Fatalf("pending entry for deleted originator was not skipped: %+v", pendingResp)
	}

	// 3. Walk-up play and Ambient Display (both ambient=true and ambient=false without play-media) record nothing.
	if _, err := st.SetPlayerWalkUp(ctx, player.ID, true); err != nil {
		t.Fatal(err)
	}
	walkUpInfo := displayPlaybackInfo(t, st, pb, *clock, displaySess2, "media-alpha")
	if walkUpInfo.Decision == playback.DecisionUnavailable || walkUpInfo.GrantID == "" {
		t.Fatalf("walk-up playbackInfo = %+v, want playable walk-up grant", walkUpInfo)
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 120, "durationSeconds": 120,
		"finished": true, "grantID": walkUpInfo.GrantID, "sequence": 0,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatalf("walk-up finish report errors = %#v", errs)
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 40, "durationSeconds": 120, "ambient": true,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatalf("ambient=true report errors = %#v", errs)
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 40, "durationSeconds": 120, "ambient": false,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatalf("ambient audio-axis report errors = %#v", errs)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, player.ID, "media-alpha"); ok {
		t.Fatal("Player Profile recorded progress during walk-up/ambient")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha"); ok {
		t.Fatal("Alice falsely attributed during walk-up/ambient")
	}

	// 4. endPlayback ends the active grant so subsequent reports are dropped, and
	//    removing controller status before becoming current skips the pending entry.
	if _, err := st.SetPlayerWalkUp(ctx, player.ID, false); err != nil {
		t.Fatal(err)
	}
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess2), "expectedRevision": 2, "commandID": "cmd_alice_end_test_1",
		"command": "play-media", "mediaID": "media-alpha",
	}, authAlice)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	endInfo := displayPlaybackInfo(t, st, pb, *clock, displaySess2, "media-alpha")
	if endInfo.GrantID == "" {
		t.Fatal("expected grantID before endPlayback")
	}
	endReq := httptest.NewRequest(http.MethodPost, "/api/graphql", strings.NewReader(`{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`))
	authDisplay2(endReq)
	endRec := httptest.NewRecorder()
	h.ServeHTTP(endRec, endReq)
	if endRec.Code != http.StatusOK {
		t.Fatalf("endPlayback status = %d: %s", endRec.Code, endRec.Body.String())
	}
	_, errs = reportPlaybackVars(t, h, map[string]any{
		"mediaID": "media-alpha", "positionSeconds": 70, "durationSeconds": 120,
		"grantID": endInfo.GrantID, "sequence": 0,
	}, authDisplay2)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, alice.ID, "media-alpha"); ok {
		t.Fatal("report after endPlayback wrote progress for Alice")
	}

	// Pending play-media skipped if originator is removed from controllers before becoming current.
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess2), "expectedRevision": 3, "commandID": "cmd_alice_demote_01",
		"command": "play-media", "mediaID": "media-alpha",
	}, authAlice)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, []store.ProfileID{alice.ID}); err != nil {
		t.Fatal(err)
	}
	excludedResp := displayPlaybackInfo(t, st, pb, *clock, displaySess2, "media-alpha")
	if excludedResp.Decision != playback.DecisionUnavailable || excludedResp.GrantID != "" {
		t.Fatalf("excluded controller pending entry was not skipped: %+v", excludedResp)
	}
	_ = authDisplay
}
