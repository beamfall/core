package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// controlSessionVars drives controlSession with an arbitrary variables object
// so the per-command arguments (CORE-1678) can be exercised.
func controlSessionVars(t *testing.T, handler *Handler, vars map[string]any, authorize func(*http.Request)) (map[string]json.RawMessage, []graphError) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"query": "mutation C($sessionID: String!, $command: String!, $direction: String, $positionMs: Float, $volume: Int, " +
			"$trackID: String, $mediaID: String, $text: String){ controlSession(sessionID:$sessionID, command:$command, " +
			"direction:$direction, positionMs:$positionMs, volume:$volume, trackID:$trackID, mediaID:$mediaID, text:$text){ sessionID command } }",
		"variables": vars,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return post(t, handler, string(raw), authorize)
}

func sessionControlPayloads(t *testing.T, st *store.Store) []store.SessionControlPayload {
	t.Helper()
	events, err := st.ReadEventOutbox(t.Context(), 0, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	var out []store.SessionControlPayload
	for _, e := range events {
		if e.AggregateType != store.OutboxAggregateSession {
			continue
		}
		var p store.SessionControlPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

// TestControlSessionArgumentVerbs publishes every argument-bearing transport
// verb against a same-Profile active session and checks the arguments reach
// the outbox payload the receiving device reads.
//
// feature:cross-device-session-control
func TestControlSessionArgumentVerbs(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	cases := []struct {
		vars  map[string]any
		check func(store.SessionControlPayload) bool
	}{
		{map[string]any{"command": "stop"}, func(p store.SessionControlPayload) bool { return p.Command == "stop" }},
		{map[string]any{"command": "skip-media", "direction": "previous"}, func(p store.SessionControlPayload) bool { return p.Direction == "previous" }},
		{map[string]any{"command": "skip-track"}, func(p store.SessionControlPayload) bool { return p.Direction == "next" }},
		{map[string]any{"command": "seek", "positionMs": 90000}, func(p store.SessionControlPayload) bool { return p.PositionMs != nil && *p.PositionMs == 90000 }},
		{map[string]any{"command": "set-volume", "volume": 40}, func(p store.SessionControlPayload) bool { return p.Volume != nil && *p.Volume == 40 }},
		{map[string]any{"command": "mute"}, func(p store.SessionControlPayload) bool { return p.Command == "mute" }},
		{map[string]any{"command": "unmute"}, func(p store.SessionControlPayload) bool { return p.Command == "unmute" }},
		{map[string]any{"command": "select-audio-track", "trackID": "a2"}, func(p store.SessionControlPayload) bool { return p.TrackID != nil && *p.TrackID == "a2" }},
		{map[string]any{"command": "select-subtitle-track", "trackID": nil}, func(p store.SessionControlPayload) bool { return p.TrackID != nil && *p.TrackID == "" }},
		{map[string]any{"command": "display-message", "text": "Dinner is ready"}, func(p store.SessionControlPayload) bool { return p.Text == "Dinner is ready" }},
	}
	for _, tc := range cases {
		tc.vars["sessionID"] = "session-default"
		if _, errs := controlSessionVars(t, handler, tc.vars, authorizeManager); len(errs) != 0 {
			t.Fatalf("controlSession(%v) errors = %#v", tc.vars, errs)
		}
	}
	payloads := sessionControlPayloads(t, st)
	if len(payloads) != len(cases) {
		t.Fatalf("outbox payloads = %d, want %d", len(payloads), len(cases))
	}
	for i, p := range payloads {
		if !cases[i].check(p) {
			t.Errorf("payload %d for %v = %#v", i, cases[i].vars, p)
		}
		if p.ProfileID != "profile-default" || p.MediaID != "media-alpha" || p.FromDevice != "device-mac" {
			t.Errorf("payload %d anchor = %q/%q/%q", i, p.ProfileID, p.MediaID, p.FromDevice)
		}
	}
}

// TestControlSessionRejectsBadArguments: a malformed or misplaced argument is
// a client error and never an outbox row.
//
// feature:cross-device-session-control
func TestControlSessionRejectsBadArguments(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	cases := []struct {
		vars map[string]any
		want string
	}{
		{map[string]any{"command": "seek"}, "seek requires positionMs"},
		{map[string]any{"command": "seek", "positionMs": 1.5}, "positionMs must be a whole number"},
		{map[string]any{"command": "seek", "positionMs": "10"}, "positionMs must be a whole number"},
		{map[string]any{"command": "set-volume", "volume": 101}, "volume must be between 0 and 100"},
		{map[string]any{"command": "pause", "positionMs": 5}, "does not take that argument"},
		{map[string]any{"command": "skip-track", "direction": "sideways"}, "direction must be next or previous"},
		{map[string]any{"command": "skip-track", "direction": 7}, "direction must be a string"},
		{map[string]any{"command": "play-media", "mediaID": 7}, "mediaID must be a string"},
		{map[string]any{"command": "display-message", "text": true}, "text must be a string"},
		{map[string]any{"command": "select-audio-track"}, "trackID"},
		{map[string]any{"command": "select-audio-track", "trackID": 7}, "trackID must be a string"},
		{map[string]any{"command": "display-message", "text": "  "}, "text"},
		{map[string]any{"command": "display-message", "text": "ring\u0007"}, "text"},
	}
	for _, tc := range cases {
		tc.vars["sessionID"] = "session-default"
		_, errs := controlSessionVars(t, handler, tc.vars, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errsJoin(errs), tc.want) {
			t.Errorf("controlSession(%v) errors = %#v, want %q", tc.vars, errs, tc.want)
		}
	}
	if payloads := sessionControlPayloads(t, st); len(payloads) != 0 {
		t.Fatalf("rejected commands wrote outbox rows: %#v", payloads)
	}
}

// TestControlSessionPlayMedia is "play on": a same-Profile device starts Media
// on another, idle device. The Media must be visible to the caller (an unknown
// and a hidden Media answer identically), the target must be in the caller's
// Profile, and an unrevealed caller cannot take over a session playing Media
// it cannot see.
//
// feature:cross-device-session-control
func TestControlSessionPlayMedia(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Idle target: session-default has no active playback.
	if _, errs := controlSessionVars(t, handler, map[string]any{
		"sessionID": "session-default", "command": "play-media", "mediaID": "media-alpha", "positionMs": 5000,
	}, authorizeManager); len(errs) != 0 {
		t.Fatalf("play-media errors = %#v", errs)
	}
	payloads := sessionControlPayloads(t, st)
	if len(payloads) != 1 || payloads[0].MediaID != "media-alpha" || payloads[0].PositionMs == nil || *payloads[0].PositionMs != 5000 {
		t.Fatalf("play-media payload = %#v", payloads)
	}

	// Hidden (restricted, unrevealed) and unknown Media answer the same.
	for _, mediaID := range []string{"media-archive", "media-missing"} {
		_, errs := controlSessionVars(t, handler, map[string]any{
			"sessionID": "session-default", "command": "play-media", "mediaID": mediaID,
		}, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errsJoin(errs), "unknown media") {
			t.Fatalf("play-media(%s) errors = %#v, want unknown media", mediaID, errs)
		}
	}

	// A revealed caller can start the restricted Media.
	if _, errs := controlSessionVars(t, handler, map[string]any{
		"sessionID": "session-manager", "command": "play-media", "mediaID": "media-archive",
	}, authorizeRevealed); len(errs) != 0 {
		t.Fatalf("revealed play-media errors = %#v", errs)
	}

	// Cross-Profile target stays unknown.
	_, errs := controlSessionVars(t, handler, map[string]any{
		"sessionID": "session-default", "command": "play-media", "mediaID": "media-alpha",
	}, authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errsJoin(errs), "unknown session") {
		t.Fatalf("cross-Profile play-media errors = %#v, want unknown session", errs)
	}

	// An unrevealed caller cannot replace a playback of Media it cannot see.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)
	_, errs = controlSessionVars(t, handler, map[string]any{
		"sessionID": "session-default", "command": "play-media", "mediaID": "media-alpha",
	}, authorizeManager)
	if len(errs) == 0 || !strings.Contains(errsJoin(errs), "not controllable") {
		t.Fatalf("play-media over hidden playback errors = %#v, want not controllable", errs)
	}

	if got := len(sessionControlPayloads(t, st)); got != 2 {
		t.Fatalf("outbox payloads = %d, want 2 (only the authorized play-media commands)", got)
	}
}
