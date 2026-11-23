package graph

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func setupPlayerDisplayFixture(t *testing.T, st *store.Store, h *Handler, clock *time.Time) (player store.Profile, displaySession store.SessionID) {
	t.Helper()
	ctx := context.Background()
	all := store.LibraryFilter{Revealed: true, AllLibraries: true}
	var err error
	player, err = st.CreateProfile(ctx, "Karaoke Room", store.ProfilePlayer, all)
	if err != nil {
		t.Fatalf("CreateProfile(player): %v", err)
	}
	displaySession = "session-karaoke-tv"
	if err := st.EnrollDevice(ctx,
		store.Device{
			ID: "device-karaoke-tv", Name: "Apple TV", Role: store.DeviceRoleViewer,
			Surface: store.DeviceSurfaceTVOSViewer, SecretHash: store.HashDeviceSecret("tv-secret"),
			PlayerProfileID: player.ID, PlayerDisplayName: "Karaoke room TV", PlayerBindingGeneration: 1,
		},
		store.Session{
			ID: displaySession, DeviceID: "device-karaoke-tv", ProfileID: player.ID,
			ExpiresAt: clock.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatalf("EnrollDevice: %v", err)
	}
	h.EventStreamOpened(displaySession)
	return player, displaySession
}

func TestPlayerControllersGraphOwnerOnlyAndProfileField(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	h := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	player, err := st.CreateProfile(ctx, "Garage TV", store.ProfilePlayer, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	// Non-admin cannot call playerControllers or setPlayerControllers.
	_, errs := post(t, h,
		`{"query":"query C($p: String!){ playerControllers(profileID: $p){ allAdults include exclude } }","variables":{"p":"`+string(player.ID)+`"}}`,
		authorizeViewer)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("non-admin playerControllers = %#v, want FORBIDDEN", errs)
	}
	_, errs = post(t, h,
		`{"query":"mutation S($p: String!, $a: Boolean){ setPlayerControllers(profileID: $p, allAdults: $a){ id } }","variables":{"p":"`+string(player.ID)+`","a":false}}`,
		authorizeViewer)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("non-admin setPlayerControllers = %#v, want FORBIDDEN", errs)
	}

	// Owner reads via createProfile / setPlayerWalkUp / playerControllers / setPlayerControllers.
	data, errs := post(t, h,
		`{"query":"mutation C($name: String!, $kind: String!){ createProfile(name: $name, kind: $kind){ id kind playerControllers { profileID allAdults include exclude } } }","variables":{"name":"Lounge TV","kind":"player"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("createProfile(player) errors = %#v", errs)
	}
	var created ProfileInfo
	mustUnmarshal(t, data["createProfile"], &created)
	if created.PlayerControllers == nil || !created.PlayerControllers.AllAdults {
		t.Fatalf("createProfile playerControllers = %+v, want allAdults=true", created.PlayerControllers)
	}

	data, errs = post(t, h,
		`{"query":"mutation S($p: String!, $a: Boolean, $i: [String!], $e: [String!]){ setPlayerControllers(profileID: $p, allAdults: $a, include: $i, exclude: $e){ id playerControllers { allAdults include exclude } } }","variables":{"p":"`+created.ID+`","a":false,"i":["profile-managed"],"e":["profile-default"]}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setPlayerControllers errors = %#v", errs)
	}
	var updated ProfileInfo
	mustUnmarshal(t, data["setPlayerControllers"], &updated)
	if updated.PlayerControllers == nil || updated.PlayerControllers.AllAdults ||
		len(updated.PlayerControllers.Include) != 1 || updated.PlayerControllers.Include[0] != "profile-managed" ||
		len(updated.PlayerControllers.Exclude) != 1 || updated.PlayerControllers.Exclude[0] != "profile-default" {
		t.Fatalf("setPlayerControllers = %+v", updated.PlayerControllers)
	}

	data, errs = post(t, h,
		`{"query":"query C($p: String!){ playerControllers(profileID: $p){ profileID allAdults include exclude } }","variables":{"p":"`+created.ID+`"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("playerControllers errors = %#v", errs)
	}
	var readBack PlayerControllersInfo
	mustUnmarshal(t, data["playerControllers"], &readBack)
	if readBack.AllAdults || len(readBack.Include) != 1 || readBack.Include[0] != "profile-managed" {
		t.Fatalf("playerControllers readBack = %+v", readBack)
	}
}

func TestPlayerControlNonLeakingRefusalsAndVerbTable(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	clock := time.Now().UTC().Truncate(time.Millisecond)
	h := newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return clock }

	// Create a plugin-bound library so certification ceilings apply to it.
	certLib, err := st.CreateLibraryWithRoots(ctx, "Cert Library", false, "demo", "profile-default", []string{t.TempDir()}, "")
	if err != nil {
		t.Fatal(err)
	}
	mediaLockedID, err := st.CreateManualMedia(ctx, store.ManualMedia{
		LibraryID: certLib.ID, Title: "Locked Film",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, mediaLockedID, []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "high"},
	}); err != nil {
		t.Fatal(err)
	}
	mediaBetaID, err := st.CreateManualMedia(ctx, store.ManualMedia{
		LibraryID: certLib.ID, Title: "Beta Media",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMediaFields(ctx, mediaBetaID, []store.MediaField{
		{Name: "certification", Type: store.EntityFieldText, Text: "low"},
	}); err != nil {
		t.Fatal(err)
	}

	h.auth.SetCertificationScales(map[string][]string{"demo": {"low", "high"}})
	if _, err := st.SetProfileLibraries(ctx, "profile-default", []store.LibraryID{"library-local", "library-archive", certLib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{"library-local", certLib.ID}); err != nil {
		t.Fatal(err)
	}
	// Put profile-managed on ceiling "low" with FilterVisibility "locked" -> can see
	// mediaLockedID under LibraryFilter() but PlaybackFilter() denies it.
	if _, err := st.SetProfileCertificationCeiling(ctx, "profile-managed", store.ProfileCertificationCeiling{
		PluginID: "demo", Ceiling: "low", FilterVisibility: "locked",
	}); err != nil {
		t.Fatal(err)
	}

	player, displaySess := setupPlayerDisplayFixture(t, st, h, &clock)
	if _, err := st.SetProfileLibraries(ctx, player.ID, []store.LibraryID{"library-local", certLib.ID}); err != nil {
		t.Fatal(err)
	}

	// 1. Non-controller (profile-managed, since allAdults=true excludes managed)
	// gets "unknown session" for nonexistent, unbound, offline, stale revision,
	// and replayed/reused commandID.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	// First let controller apply one command at rev 0 -> rev 1 with cmd_ctrl_seed_0001.
	data, errs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 0,
		"commandID":        "cmd_ctrl_seed_0001",
		"command":          "display-message",
		"text":             "Ready",
	}, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("seed controller display-message errors = %#v", errs)
	}
	var seedRes ControlRendererSessionResult
	mustUnmarshal(t, data["controlRendererSession"], &seedRes)
	if seedRes.Status != "APPLIED" || seedRes.Revision != 1 {
		t.Fatalf("seedRes = %+v", seedRes)
	}

	// Now non-controller tests every refusal shape.
	nonCtrlCases := []struct {
		name string
		vars map[string]any
	}{
		{
			name: "nonexistent session",
			vars: map[string]any{"sessionID": "session-nope", "expectedRevision": 0, "commandID": "cmd_nonctrl_00001", "command": "stop"},
		},
		{
			name: "unbound ordinary session",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": "cmd_nonctrl_00002", "command": "stop"},
		},
		{
			name: "stale revision on player session",
			vars: map[string]any{"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_nonctrl_00003", "command": "stop"},
		},
		{
			name: "replayed commandID from controller",
			vars: map[string]any{"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_ctrl_seed_0001", "command": "display-message", "text": "Ready"},
		},
		{
			name: "reused commandID with different body",
			vars: map[string]any{"sessionID": string(displaySess), "expectedRevision": 1, "commandID": "cmd_ctrl_seed_0001", "command": "stop"},
		},
	}
	for _, tc := range nonCtrlCases {
		t.Run("non-controller/"+tc.name, func(t *testing.T) {
			_, errs := controlRendererSessionVars(t, h, tc.vars, authorizeManaged)
			if len(errs) == 0 || errs[0].Message != "unknown session" || errs[0].Extensions.Code != ErrorCodeNotFound {
				t.Fatalf("errors = %#v, want NOT_FOUND unknown session", errs)
			}
		})
	}
	// Non-controller also gets "unknown session" when display is offline.
	h.EventStreamClosed(displaySess)
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 1, "commandID": "cmd_nonctrl_off_1", "command": "stop",
	}, authorizeManaged)
	if len(errs) == 0 || errs[0].Message != "unknown session" || errs[0].Extensions.Code != ErrorCodeNotFound {
		t.Fatalf("non-controller offline errors = %#v, want NOT_FOUND unknown session", errs)
	}
	h.EventStreamOpened(displaySess)

	// 2. Include profile-managed as a controller, and test D4 verb table.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []store.ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatal(err)
	}

	// Managed controller CAN see mediaLockedID (locked), CANNOT play it -> play-media refused!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 1,
		"commandID":        "cmd_managed_locked_play",
		"command":          "play-media",
		"mediaID":          string(mediaLockedID),
	}, authorizeManaged)
	if len(errs) == 0 || errs[0].Message != "unknown media" {
		t.Fatalf("managed locked play-media = %#v, want unknown media", errs)
	}

	// Revealed adult controller CAN play restricted media-archive, display CANNOT -> refused!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 1,
		"commandID":        "cmd_adult_restricted_play",
		"command":          "play-media",
		"mediaID":          "media-archive",
	}, authorizeRevealed)
	if len(errs) == 0 || errs[0].Message != "display cannot play this media" || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("revealed adult restricted play-media = %#v, want FORBIDDEN display cannot play this media", errs)
	}

	// Managed controller CAN play mediaBetaID (both pass PlaybackFilter) -> APPLIED rev 2!
	data, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 1,
		"commandID":        "cmd_managed_beta_play",
		"command":          "play-media",
		"mediaID":          string(mediaBetaID),
	}, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed play-media(media-beta) errors = %#v", errs)
	}
	var playRes ControlRendererSessionResult
	mustUnmarshal(t, data["controlRendererSession"], &playRes)
	if playRes.Status != "APPLIED" || playRes.Revision != 2 {
		t.Fatalf("playRes = %+v", playRes)
	}

	// Start active stream on display playing mediaLockedID (locked for managed, playable by adult).
	if err := st.RecordStreamToken(ctx, displaySess, player.ID, "device-karaoke-tv", "media-direct", mediaLockedID, false, clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Pause: mediaLockedID passes managed's LibraryFilter() (locked is visible) -> ALLOWED!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 2,
		"commandID":        "cmd_managed_pause_locked",
		"command":          "pause",
	}, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed pause on locked item errors = %#v", errs)
	}
	// Resume: requires PlaybackFilter() on current item -> managed REFUSED!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 3,
		"commandID":        "cmd_managed_resume_locked",
		"command":          "resume",
	}, authorizeManaged)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("managed resume on locked item = %#v, want FORBIDDEN", errs)
	}

	// Revoke library-local from profile-managed so current item media-alpha is completely INVISIBLE.
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{}); err != nil {
		t.Fatal(err)
	}
	// Pause on invisible item -> REFUSED!
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 3,
		"commandID":        "cmd_managed_pause_invis",
		"command":          "pause",
	}, authorizeManaged)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("managed pause on invisible item = %#v, want FORBIDDEN", errs)
	}
	// Stop on invisible item -> ALWAYS ALLOWED for controllers!
	data, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 3,
		"commandID":        "cmd_managed_stop_invis",
		"command":          "stop",
	}, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed stop on invisible item errors = %#v", errs)
	}
	var stopRes ControlRendererSessionResult
	mustUnmarshal(t, data["controlRendererSession"], &stopRes)
	if stopRes.Status != "APPLIED" || stopRes.Revision != 4 {
		t.Fatalf("stopRes = %+v", stopRes)
	}

	// Verify cross-Profile activity row recorded with controller Profile ("profile-managed").
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{
		Filter:    store.LibraryFilter{AllLibraries: true, Revealed: true},
		ProfileID: "profile-managed",
		Limit:     20,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundAudit := false
	for _, entry := range entries {
		if entry.Action == store.ActivitySessionControl && entry.ProfileID == "profile-managed" && entry.TargetID == string(displaySess) {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("missing session.control activity row attributed to controller profile-managed: %+v", entries)
	}
}

func TestPlayerDisplaysObservationPrivateItemAndPresence(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	clock := time.Now().UTC().Truncate(time.Millisecond)
	h := newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return clock }

	player, displaySess := setupPlayerDisplayFixture(t, st, h, &clock)
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []store.ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatal(err)
	}

	// Non-controller (after removing profile-managed) sees nothing in playerDisplays.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, errs := post(t, h, `{"query":"query { playerDisplays { handle sessionID displayName presence } }"}`, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("non-controller playerDisplays errors = %#v", errs)
	}
	var emptyDisplays []PlayerDisplayInfo
	mustUnmarshal(t, data["playerDisplays"], &emptyDisplays)
	if len(emptyDisplays) != 0 {
		t.Fatalf("non-controller saw displays: %+v", emptyDisplays)
	}

	// Re-include profile-managed, and strip its library access so media-alpha is invisible to it.
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, []store.ProfileID{"profile-managed"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetProfileLibraries(ctx, "profile-managed", []store.LibraryID{}); err != nil {
		t.Fatal(err)
	}

	// Ready state before playback starts.
	data, errs = post(t, h, `{"query":"query { playerDisplays { handle sessionID playerProfileID displayName presence lastSeenAt currentItem { mediaID title libraryID artworkURL privateItem label positionSeconds durationSeconds } } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("viewer playerDisplays errors = %#v", errs)
	}
	var viewerDisplays []PlayerDisplayInfo
	mustUnmarshal(t, data["playerDisplays"], &viewerDisplays)
	if len(viewerDisplays) != 1 || viewerDisplays[0].Presence != "ready" || viewerDisplays[0].DisplayName != "Karaoke room TV" || viewerDisplays[0].Handle == "device-karaoke-tv" || viewerDisplays[0].Handle == "" {
		t.Fatalf("viewer ready playerDisplays = %+v", viewerDisplays)
	}

	// Start playback of media-alpha with stored progress on player profile.
	if err := st.RecordStreamToken(ctx, displaySess, player.ID, "device-karaoke-tv", "media-direct", "media-alpha", false, clock.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackProgress(ctx, store.PlaybackProgress{
		ProfileID: player.ID, MediaID: "media-alpha", PositionSeconds: 42, DurationSeconds: 120,
	}, false, store.ResumePolicy{Class: store.MediaClassVideo}); err != nil {
		t.Fatal(err)
	}

	// Viewer (adult with library-local) sees full title/mediaID/libraryID/position/duration.
	data, errs = post(t, h, `{"query":"query { playerDisplays { handle sessionID presence currentItem { mediaID title libraryID artworkURL privateItem label positionSeconds durationSeconds } } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("viewer playing playerDisplays errors = %#v", errs)
	}
	mustUnmarshal(t, data["playerDisplays"], &viewerDisplays)
	if len(viewerDisplays) != 1 || viewerDisplays[0].Presence != "playing" || viewerDisplays[0].CurrentItem == nil {
		t.Fatalf("viewer playing = %+v", viewerDisplays)
	}
	ci := viewerDisplays[0].CurrentItem
	if ci.MediaID != "media-alpha" || ci.Title != "Sample Media" || ci.LibraryID != "library-local" || ci.PrivateItem || ci.PositionSeconds != 42 || ci.DurationSeconds != 120 {
		t.Fatalf("viewer currentItem = %+v", ci)
	}

	// Managed controller (without library-local) sees "Private item" with position & duration only!
	data, errs = post(t, h, `{"query":"query { playerDisplays { handle sessionID presence currentItem { mediaID title libraryID artworkURL privateItem label positionSeconds durationSeconds } } }"}`, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed playing playerDisplays errors = %#v", errs)
	}
	var managedDisplays []PlayerDisplayInfo
	mustUnmarshal(t, data["playerDisplays"], &managedDisplays)
	if len(managedDisplays) != 1 || managedDisplays[0].Presence != "playing" || managedDisplays[0].CurrentItem == nil {
		t.Fatalf("managed playing = %+v", managedDisplays)
	}
	mci := managedDisplays[0].CurrentItem
	if !mci.PrivateItem || mci.Label != "Private item" || mci.MediaID != "" || mci.Title != "" || mci.LibraryID != "" || mci.ArtworkURL != "" || mci.PositionSeconds != 42 || mci.DurationSeconds != 120 {
		t.Fatalf("managed Private item currentItem = %+v", mci)
	}

	// Conflict snapshot on stale expectedRevision also applies D6 per observing controller.
	// Advance rev 0 -> 1 via stop.
	if _, errs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_snap_seed_0001", "command": "stop",
	}, authorizeViewer); len(errs) != 0 {
		t.Fatalf("seed stop errors = %#v", errs)
	}
	data, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_snap_conflict_1", "command": "stop",
	}, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed conflict errors = %#v", errs)
	}
	var conflictRes ControlRendererSessionResult
	mustUnmarshal(t, data["controlRendererSession"], &conflictRes)
	if conflictRes.Status != "CONFLICT" || conflictRes.Snapshot == nil || len(*conflictRes.Snapshot) != 1 {
		t.Fatalf("managed conflictRes = %+v", conflictRes)
	}
	snapRow := (*conflictRes.Snapshot)[0]
	if !snapRow.PrivateItem || snapRow.Label != "Private item" || snapRow.MediaID != "" || snapRow.Title != "" || snapRow.LibraryID != "" || snapRow.ArtworkURL != "" || snapRow.PositionSeconds != 42 || snapRow.DurationSeconds != 120 {
		t.Fatalf("managed conflict snapshot leaked or dropped position: %+v", snapRow)
	}

	// Heartbeat expiry after 60 s: presence becomes offline, command returns "display offline".
	clock = clock.Add(59 * time.Second)
	h.EventStreamHeartbeat(displaySess) // touch at +59s refreshes window
	clock = clock.Add(59 * time.Second) // 59s since last touch -> still online!
	data, errs = post(t, h, `{"query":"query { playerDisplays { presence } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	mustUnmarshal(t, data["playerDisplays"], &viewerDisplays)
	if viewerDisplays[0].Presence != "playing" {
		t.Fatalf("presence at 59s after heartbeat = %q, want playing", viewerDisplays[0].Presence)
	}

	clock = clock.Add(2 * time.Second) // 61s since last heartbeat -> offline!
	data, errs = post(t, h, `{"query":"query { playerDisplays { presence lastSeenAt currentItem { privateItem } } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	viewerDisplays = nil
	mustUnmarshal(t, data["playerDisplays"], &viewerDisplays)
	if viewerDisplays[0].Presence != "offline" || viewerDisplays[0].LastSeenAt == "" || viewerDisplays[0].CurrentItem != nil {
		t.Fatalf("presence at 61s without heartbeat = %+v, want offline with lastSeenAt and nil currentItem", viewerDisplays[0])
	}
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 1, "commandID": "cmd_offline_ctrl_1", "command": "stop",
	}, authorizeViewer)
	if len(errs) == 0 || errs[0].Message != "display offline" || errs[0].Extensions.Code != ErrorCodeConflict || errs[0].Extensions.Reason != ErrorReasonDisplayOffline {
		t.Fatalf("offline controller errors = %#v, want CONFLICT/DISPLAY_OFFLINE display offline", errs)
	}
}

func TestPlayerControlRateLimitPerControllerProfile(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	clock := time.Now().UTC().Truncate(time.Millisecond)
	h := newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return clock }

	player, displaySess := setupPlayerDisplayFixture(t, st, h, &clock)
	adult2, err := st.CreateProfile(ctx, "Second Adult", store.ProfileAdult, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EnrollDevice(ctx,
		store.Device{ID: "device-adult2", Name: "Phone", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("a2-secret")},
		store.Session{ID: "session-adult2", DeviceID: "device-adult2", ProfileID: adult2.ID, ExpiresAt: clock.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	authorizeAdult2 := func(req *http.Request) {
		req.Header.Set("X-Beamfall-Session", "session-adult2")
		req.Header.Set("X-Beamfall-Device", "device-adult2")
		req.Header.Set("X-Beamfall-Device-Secret", "a2-secret")
	}
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}

	// 30 commands in <10 s succeed for profile-default.
	for i := 0; i < 30; i++ {
		_, errs := controlRendererSessionVars(t, h, map[string]any{
			"sessionID":        string(displaySess),
			"expectedRevision": i,
			"commandID":        "cmd_rate_limit_" + string(rune('a'+i/26)) + string(rune('a'+i%26)) + "_01",
			"command":          "stop",
		}, authorizeViewer)
		if len(errs) != 0 {
			t.Fatalf("command %d unexpected error = %#v", i+1, errs)
		}
	}

	// 31st command within 10 s is throttled with THROTTLED.
	_, errs := controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 30,
		"commandID":        "cmd_rate_limit_31_01",
		"command":          "stop",
	}, authorizeViewer)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeThrottled || !strings.Contains(errs[0].Message, "throttled") {
		t.Fatalf("31st command errors = %#v, want THROTTLED", errs)
	}

	// A separate controller Profile is not throttled by profile-default's bucket.
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 30,
		"commandID":        "cmd_rate_limit_other_1",
		"command":          "stop",
	}, authorizeAdult2)
	if len(errs) != 0 {
		t.Fatalf("second controller errors = %#v", errs)
	}

	// After the 10 s window passes, profile-default can command again.
	clock = clock.Add(21 * time.Second)
	h.EventStreamHeartbeat(displaySess)
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID":        string(displaySess),
		"expectedRevision": 31,
		"commandID":        "cmd_rate_limit_after_1",
		"command":          "stop",
	}, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("post-window command errors = %#v", errs)
	}

	// Unused auth import guard if needed.
	_ = auth.Scope{}
}

// ADR-0240 D3: the legacy controlSession stays same-Profile and refuses every
// player target, even for a listed controller; only controlRendererSession
// drives a display.
func TestLegacyControlSessionRefusesPlayerTargetForController(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	clock := time.Now().UTC().Truncate(time.Millisecond)
	h := newTestHandlerWithStore(st)
	h.nowFunc = func() time.Time { return clock }

	player, displaySess := setupPlayerDisplayFixture(t, st, h, &clock)
	if _, err := st.SetPlayerControllers(ctx, player.ID, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	body := `{"query":"mutation { controlSession(sessionID: \"` + string(displaySess) + `\", command: \"stop\") { command } }"}`
	_, errs := post(t, h, body, authorizeViewer)
	if len(errs) == 0 || errs[0].Message != "unknown session" {
		t.Fatalf("legacy controlSession on a player target errors = %#v, want unknown session", errs)
	}
	_, errs = controlRendererSessionVars(t, h, map[string]any{
		"sessionID": string(displaySess), "expectedRevision": 0, "commandID": "cmd_legacy_contrast_01", "command": "stop",
	}, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("controlRendererSession by the same controller errors = %#v", errs)
	}
}
