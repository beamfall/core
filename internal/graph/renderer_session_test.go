package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func controlRendererSessionVars(t *testing.T, handler *Handler, vars map[string]any, authorize func(*http.Request)) (map[string]json.RawMessage, []graphError) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"query": "mutation C($sessionID: String!, $expectedRevision: Int!, $commandID: String!, $command: String!, " +
			"$direction: String, $positionMs: Float, $volume: Int, $trackID: String, $mediaID: String, $text: String){ " +
			"controlRendererSession(sessionID:$sessionID, expectedRevision:$expectedRevision, commandID:$commandID, command:$command, " +
			"direction:$direction, positionMs:$positionMs, volume:$volume, trackID:$trackID, mediaID:$mediaID, text:$text){ " +
			"status sessionID command revision snapshot { sessionID deviceID deviceName deviceRole current mediaID title libraryID transcoding positionSeconds durationSeconds revision } } }",
		"variables": vars,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return post(t, handler, string(raw), authorize)
}

func TestControlRendererSessionApplyReplayConflictAndProfileSessionsRevision(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)

	// profileSessions starts at revision 0.
	data, errs := post(t, handler, `{"query":"query { profileSessions { sessionID revision } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("profileSessions errors = %#v", errs)
	}
	var sessions []ProfileSessionInfo
	if err := json.Unmarshal(data["profileSessions"], &sessions); err != nil {
		t.Fatalf("decode profileSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "session-default" || sessions[0].Revision != 0 {
		t.Fatalf("initial profileSessions = %#v, want session-default rev 0", sessions)
	}

	// Apply at expectedRevision 0 -> APPLIED rev 1, no snapshot.
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 0,
		"commandID":        "cmd_graph_apply_01",
		"command":          "pause",
	}, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("controlRendererSession apply errors = %#v", errs)
	}
	var res ControlRendererSessionResult
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatalf("decode controlRendererSession: %v", err)
	}
	if res.Status != "APPLIED" || res.SessionID != "session-default" || res.Command != "pause" || res.Revision != 1 || res.Snapshot != nil {
		t.Fatalf("apply result = %#v, want APPLIED rev 1 nil snapshot", res)
	}

	// Idempotent replay with same commandID + body returns APPLIED rev 1 with no second outbox event.
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 0,
		"commandID":        "cmd_graph_apply_01",
		"command":          "pause",
	}, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("replay errors = %#v", errs)
	}
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if res.Status != "APPLIED" || res.Revision != 1 {
		t.Fatalf("replay result = %#v, want APPLIED rev 1", res)
	}

	// Reusing commandID with a different body fails with CONFLICT error code.
	_, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 1,
		"commandID":        "cmd_graph_apply_01",
		"command":          "resume",
	}, authorizeManager)
	if len(errs) == 0 || errs[0].Extensions.Code != ErrorCodeConflict {
		t.Fatalf("commandID reuse errors = %#v, want CONFLICT", errs)
	}

	// Legacy controlSession also bumps the revision (1 -> 2).
	if _, errs := controlSessionPost(t, handler, "session-default", "resume", authorizeManager); len(errs) != 0 {
		t.Fatalf("legacy controlSession errors = %#v", errs)
	}

	// profileSessions now reports revision 2.
	data, errs = post(t, handler, `{"query":"query { profileSessions { sessionID revision } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("profileSessions after legacy errors = %#v", errs)
	}
	if err := json.Unmarshal(data["profileSessions"], &sessions); err != nil {
		t.Fatalf("decode profileSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Revision != 2 {
		t.Fatalf("profileSessions after legacy = %#v, want rev 2", sessions)
	}

	// Stale expectedRevision (1 vs 2) returns CONFLICT with current revision 2 and
	// a snapshot scoped ONLY to session-default (never session-managed).
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 1,
		"commandID":        "cmd_graph_stale_02",
		"command":          "seek",
		"positionMs":       float64(45000),
	}, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("stale revision errors = %#v", errs)
	}
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if res.Status != "CONFLICT" || res.Revision != 2 || res.SessionID != "session-default" || res.Command != "seek" {
		t.Fatalf("conflict result = %#v, want CONFLICT rev 2", res)
	}
	if res.Snapshot == nil || len(*res.Snapshot) != 1 {
		t.Fatalf("conflict snapshot = %#v, want 1 row for session-default", res.Snapshot)
	}
	row := (*res.Snapshot)[0]
	if row.SessionID != "session-default" || row.MediaID != "media-alpha" || row.Title != "Sample Media" || row.Revision != 2 {
		t.Fatalf("conflict snapshot row = %+v, want session-default/media-alpha rev 2", row)
	}

	if got := len(sessionControlPayloads(t, st)); got != 2 {
		t.Fatalf("outbox session.control payloads = %d, want 2 (1 controlRendererSession + 1 legacy controlSession)", got)
	}
}

func TestControlRendererSessionAuthorizationGates(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Cross-Profile target is refused as "unknown session" (NOT_FOUND).
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	_, errs := controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 0,
		"commandID":        "cmd_cross_prof_001",
		"command":          "pause",
	}, authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown session") || errs[0].Extensions.Code != ErrorCodeNotFound {
		t.Fatalf("cross-Profile errors = %#v, want NOT_FOUND unknown session", errs)
	}

	// Reveal gate: unrevealed caller on restricted media gets "session not controllable" (FORBIDDEN)
	// without leaking media-archive, even if expectedRevision would have conflicted.
	payloadArchive, err := store.NewSessionControlPayload(store.SessionCommandPlayMedia, store.SessionControlArgs{MediaID: "media-archive"}, "device-mac", "profile-default", "media-archive")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSessionControlEvent(t.Context(), "session-default", payloadArchive, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)

	_, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 0,
		"commandID":        "cmd_unrevealed_001",
		"command":          "pause",
	}, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "session not controllable") || errs[0].Extensions.Code != ErrorCodeForbidden {
		t.Fatalf("unrevealed errors = %#v, want FORBIDDEN session not controllable", errs)
	}
	if strings.Contains(errsJoin(errs), "media-archive") {
		t.Fatalf("unrevealed error leaked restricted media id: %#v", errs)
	}

	// Revealed caller succeeds on the same session at current revision 1 -> APPLIED rev 2.
	data, errs := controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 1,
		"commandID":        "cmd_revealed_ok_01",
		"command":          "pause",
	}, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed errors = %#v", errs)
	}
	var res ControlRendererSessionResult
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "APPLIED" || res.Revision != 2 {
		t.Fatalf("revealed apply = %#v, want APPLIED rev 2", res)
	}

	// Revealed caller with stale revision gets CONFLICT snapshot carrying media-archive.
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-default",
		"expectedRevision": 0,
		"commandID":        "cmd_revealed_stale",
		"command":          "pause",
	}, authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed stale errors = %#v", errs)
	}
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "CONFLICT" || res.Snapshot == nil || len(*res.Snapshot) != 2 {
		t.Fatalf("revealed conflict = %#v, want 2 rows", res)
	}

	// Transport verb on idle device session fails with "session has no active playback".
	_, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-manager",
		"expectedRevision": 0,
		"commandID":        "cmd_idle_pause_001",
		"command":          "pause",
	}, authorizeViewer)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "session has no active playback") || errs[0].Extensions.Code != ErrorCodeConflict {
		t.Fatalf("idle pause errors = %#v, want CONFLICT session has no active playback", errs)
	}

	// play-media on idle device session checks Media visibility identically for missing and hidden Media.
	for _, mediaID := range []string{"media-missing", "media-archive"} {
		_, errs = controlRendererSessionVars(t, handler, map[string]any{
			"sessionID":        "session-manager",
			"expectedRevision": 0,
			"commandID":        "cmd_play_hidden_" + mediaID[6:],
			"command":          "play-media",
			"mediaID":          mediaID,
		}, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown media") || errs[0].Extensions.Code != ErrorCodeNotFound {
			t.Fatalf("play-media(%s) errors = %#v, want NOT_FOUND unknown media", mediaID, errs)
		}
	}

	// play-media on idle device session succeeds for visible Media, and a stale
	// second play-media returns CONFLICT with an empty snapshot array ([]).
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-manager",
		"expectedRevision": 0,
		"commandID":        "cmd_play_idle_0001",
		"command":          "play-media",
		"mediaID":          "media-alpha",
		"positionMs":       float64(5000),
	}, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("play-media on idle session errors = %#v", errs)
	}
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "APPLIED" || res.Revision != 1 {
		t.Fatalf("play-media on idle session = %#v, want APPLIED rev 1", res)
	}
	data, errs = controlRendererSessionVars(t, handler, map[string]any{
		"sessionID":        "session-manager",
		"expectedRevision": 0,
		"commandID":        "cmd_play_idle_0002",
		"command":          "play-media",
		"mediaID":          "media-alpha",
	}, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("play-media conflict errors = %#v", errs)
	}
	if err := json.Unmarshal(data["controlRendererSession"], &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "CONFLICT" || res.Revision != 1 || res.Snapshot == nil || len(*res.Snapshot) != 0 {
		t.Fatalf("idle conflict result = %#v, want CONFLICT rev 1 empty snapshot", res)
	}
}

func TestControlRendererSessionConflictSnapshotIsRevealFiltered(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	now := time.Now().UTC()

	// session-default (profile-default) holds grants for both open media-alpha
	// and restricted media-archive; session-managed is in another Profile.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)

	unrevealedScope := auth.Scope{
		SessionID:    "session-manager",
		ProfileID:    "profile-default",
		DeviceID:     "device-mac",
		AllLibraries: true,
		Revealed:     false,
	}
	snap, err := handler.rendererSessionSnapshot(t.Context(), unrevealedScope, "session-default", 3, now)
	if err != nil {
		t.Fatalf("rendererSessionSnapshot unrevealed: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2 rows for session-default only", len(snap))
	}
	var seenOpen, seenHidden bool
	for _, row := range snap {
		if row.SessionID != "session-default" || row.Revision != 3 {
			t.Fatalf("unexpected row session/revision: %+v", row)
		}
		if row.MediaID == "media-alpha" {
			seenOpen = true
			if row.Title != "Sample Media" || row.LibraryID == "" {
				t.Errorf("open media row missing identity: %+v", row)
			}
			continue
		}
		seenHidden = true
		if row.MediaID != "" || row.Title != "" || row.LibraryID != "" || row.PositionSeconds != 0 || row.DurationSeconds != 0 {
			t.Errorf("unrevealed snapshot leaked restricted media identity: %+v", row)
		}
	}
	if !seenOpen || !seenHidden {
		t.Fatalf("expected 1 open and 1 reveal-suppressed row, got %+v", snap)
	}

	revealedScope := auth.Scope{
		SessionID:    "session-revealed",
		ProfileID:    "profile-default",
		DeviceID:     "device-mac",
		AllLibraries: true,
		Revealed:     true,
	}
	revealedSnap, err := handler.rendererSessionSnapshot(t.Context(), revealedScope, "session-default", 3, now)
	if err != nil {
		t.Fatalf("rendererSessionSnapshot revealed: %v", err)
	}
	var sawArchive bool
	for _, row := range revealedSnap {
		if row.MediaID == "media-archive" && row.Title == "Archive Media" {
			sawArchive = true
		}
	}
	if !sawArchive {
		t.Fatalf("revealed snapshot missing media-archive: %+v", revealedSnap)
	}
}

func TestControlRendererSessionValidationErrors(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	// Send with untyped JSON query so malformed types (float, negative, missing, etc.)
	// reach the resolver's validation instead of GraphQL variable type parsing.
	callRaw := func(vars map[string]any) []graphError {
		raw, err := json.Marshal(map[string]any{
			"query":     "mutation { controlRendererSession { status sessionID command revision } }",
			"variables": vars,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, errs := post(t, handler, string(raw), authorizeManager)
		return errs
	}

	cases := []struct {
		name string
		vars map[string]any
		want string
	}{
		{
			name: "missing sessionID",
			vars: map[string]any{"expectedRevision": 0, "commandID": "cmd_valid_00000001", "command": "pause"},
			want: "sessionID is required",
		},
		{
			name: "missing expectedRevision",
			vars: map[string]any{"sessionID": "session-default", "commandID": "cmd_valid_00000001", "command": "pause"},
			want: "expectedRevision is required",
		},
		{
			name: "negative expectedRevision",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": -1, "commandID": "cmd_valid_00000001", "command": "pause"},
			want: "expectedRevision must be a non-negative integer",
		},
		{
			name: "fractional expectedRevision",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 1.5, "commandID": "cmd_valid_00000001", "command": "pause"},
			want: "expectedRevision must be a non-negative integer",
		},
		{
			name: "string expectedRevision",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": "0", "commandID": "cmd_valid_00000001", "command": "pause"},
			want: "expectedRevision must be a non-negative integer",
		},
		{
			name: "missing commandID",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "command": "pause"},
			want: "commandID is required",
		},
		{
			name: "non-string commandID",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": 12345, "command": "pause"},
			want: "commandID must be a string",
		},
		{
			name: "short commandID",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": "too-short", "command": "pause"},
			want: "commandID must be 16 to 128",
		},
		{
			name: "long commandID",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": strings.Repeat("a", 129), "command": "pause"},
			want: "commandID must be 16 to 128",
		},
		{
			name: "invalid chars commandID",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": "cmd_invalid$00001", "command": "pause"},
			want: "commandID must be 16 to 128",
		},
		{
			name: "unknown command",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": "cmd_valid_00000001", "command": "teleport"},
			want: "unknown transport command",
		},
		{
			name: "misplaced arg",
			vars: map[string]any{"sessionID": "session-default", "expectedRevision": 0, "commandID": "cmd_valid_00000001", "command": "pause", "positionMs": 10},
			want: "does not take that argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := callRaw(tc.vars)
			if len(errs) == 0 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("errors = %#v, want %q", errs, tc.want)
			}
			if errs[0].Extensions.Code != ErrorCodeInvalid {
				t.Fatalf("error code = %s, want INVALID", errs[0].Extensions.Code)
			}
		})
	}

	if payloads := sessionControlPayloads(t, st); len(payloads) != 0 {
		t.Fatalf("validation failures wrote outbox payloads: %#v", payloads)
	}
}
