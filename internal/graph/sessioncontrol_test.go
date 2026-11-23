package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// authorizeManaged drives a managed-Profile (profile-managed) session — a
// DIFFERENT Profile from the profile-default viewer/manager — so cross-Profile
// rejection can be exercised. session-managed is seeded on device-tv.
func authorizeManaged(req *http.Request) {
	req.Header.Set("X-Beamfall-Session", "session-managed")
	req.Header.Set("X-Beamfall-Device", "device-tv")
	req.Header.Set("X-Beamfall-Device-Secret", "dev-tv-secret")
}

// TestProfileSessionsEnumeratesOwnProfileOnly is the AV-3 enumeration acceptance:
// the Profile-scoped active-session read lists only the
// caller's OWN Profile's active playbacks, never another Profile's. session-
// default (profile-default) and session-managed (profile-managed) are both
// playing media-alpha; the profile-default caller sees only its own.
//
// feature:cross-device-session-control
func TestProfileSessionsEnumeratesOwnProfileOnly(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Two active playbacks in two different Profiles, both on media-alpha (open
	// library), recorded via the real Stream Token ledger the enumeration reads.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-direct", "media-alpha", false)

	data, errs := post(t, handler,
		`{"query":"query { profileSessions { sessionID current mediaID title } }"}`,
		authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("profileSessions errors = %#v", errs)
	}
	var got []ProfileSessionInfo
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode profileSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("profileSessions len = %d, want 1 (own Profile only): %#v", len(got), got)
	}
	if got[0].SessionID != "session-default" {
		t.Fatalf("listed session = %q, want session-default (own Profile)", got[0].SessionID)
	}
	if got[0].MediaID != "media-alpha" || got[0].Title != "Sample Media" {
		t.Errorf("session media = %q/%q, want media-alpha/Sample Media", got[0].MediaID, got[0].Title)
	}
	if !got[0].Current {
		t.Errorf("own session not marked current")
	}

	// The managed-Profile caller, symmetrically, sees only its own session — never
	// the profile-default one. This is the load-bearing same-Profile assertion.
	data, errs = post(t, handler, `{"query":"query { profileSessions { sessionID } }"}`, authorizeManaged)
	if len(errs) != 0 {
		t.Fatalf("managed profileSessions errors = %#v", errs)
	}
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode managed profileSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "session-managed" {
		t.Fatalf("managed caller saw %#v, want only session-managed", got)
	}
}

// TestProfileSessionsRevealSuppressesRestrictedMedia is the AV-3 enumeration
// Reveal acceptance (ADR-0035 §"Security surface"): a same-Profile session
// playing restricted-Library Media is still LISTED (the device is active) but
// its Media identity is suppressed for an unrevealed caller, and present for a
// revealed one.
//
// feature:cross-device-session-control
func TestProfileSessionsRevealSuppressesRestrictedMedia(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// session-default (profile-default) plays restricted media-archive.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)

	// An unrevealed same-Profile caller (session-manager, profile-default) lists
	// the session but with Media identity suppressed.
	data, errs := post(t, handler,
		`{"query":"query { profileSessions { sessionID mediaID title libraryID } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("unrevealed profileSessions errors = %#v", errs)
	}
	var got []ProfileSessionInfo
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode profileSessions: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "session-default" {
		t.Fatalf("unrevealed listed = %#v, want session-default", got)
	}
	if got[0].MediaID != "" || got[0].Title != "" || got[0].LibraryID != "" {
		t.Errorf("unrevealed caller leaked restricted media identity: %q/%q/%q", got[0].MediaID, got[0].Title, got[0].LibraryID)
	}

	// A revealed same-Profile caller (session-revealed) sees the Media identity.
	data, errs = post(t, handler,
		`{"query":"query { profileSessions { sessionID mediaID title libraryID } }"}`,
		authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed profileSessions errors = %#v", errs)
	}
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode revealed profileSessions: %v", err)
	}
	if len(got) != 1 || got[0].MediaID != "media-archive" || got[0].Title != "Archive Media" || got[0].LibraryID == "" {
		t.Fatalf("revealed caller media = %#v, want media-archive/Archive Media/libraryID present", got)
	}
}

// controlSession drives the transport mutation and returns the data/errors.
func controlSessionPost(t *testing.T, handler *Handler, target, command string, authorize func(*http.Request)) (map[string]json.RawMessage, []graphError) {
	t.Helper()
	body := `{"query":"mutation C($sessionID: String!, $command: String!){ controlSession(sessionID:$sessionID, command:$command){ sessionID command } }","variables":{"sessionID":"` +
		target + `","command":"` + command + `"}}`
	return post(t, handler, body, authorize)
}

// TestControlSessionSameProfileAuthorized is the AV-3 transport acceptance:
// a same-Profile device issues a transport command against
// another device's active session; the command is authorized and published on
// the event outbox (the SSE delivery is proven in the server package). The
// controlling device is session-manager; the target is session-default — both
// profile-default. Every closed-vocabulary verb is accepted.
//
// feature:cross-device-session-control
func TestControlSessionSameProfileAuthorized(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// Target session-default is actively playing media-alpha (open library).
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	for _, command := range []string{"pause", "resume", "skip-media", "skip-track"} {
		data, errs := controlSessionPost(t, handler, "session-default", command, authorizeManager)
		if len(errs) != 0 {
			t.Fatalf("controlSession(%s) errors = %#v", command, errs)
		}
		var result ControlSessionResult
		if err := json.Unmarshal(data["controlSession"], &result); err != nil {
			t.Fatalf("decode controlSession(%s): %v", command, err)
		}
		if result.SessionID != "session-default" || result.Command != command {
			t.Errorf("controlSession echo = %q/%q, want session-default/%s", result.SessionID, result.Command, command)
		}
	}

	// The commands actually landed on the outbox as session-control events
	// targeting session-default, carrying the target Profile + Media (the SSE
	// surface filters on these). Read the outbox directly — the real delivery
	// spine, not a stub.
	events, err := st.ReadEventOutbox(t.Context(), 0, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	controlCount := 0
	for _, e := range events {
		if e.AggregateType != store.OutboxAggregateSession {
			continue
		}
		controlCount++
		if e.Kind != store.SessionControlKind {
			t.Errorf("session event kind = %q, want %q", e.Kind, store.SessionControlKind)
		}
		if e.AggregateID != "session-default" {
			t.Errorf("session event target = %q, want session-default", e.AggregateID)
		}
		var p store.SessionControlPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if p.ProfileID != "profile-default" || p.MediaID != "media-alpha" {
			t.Errorf("payload profile/media = %q/%q, want profile-default/media-alpha", p.ProfileID, p.MediaID)
		}
		if len(p.PlayingMediaIDs) != 1 || p.PlayingMediaIDs[0] != "media-alpha" {
			t.Errorf("payload playingMediaIDs = %v, want [media-alpha]", p.PlayingMediaIDs)
		}
	}
	if controlCount != 4 {
		t.Errorf("session-control events = %d, want 4", controlCount)
	}

	// An unknown verb is a client error, never an outbox row.
	_, errs := controlSessionPost(t, handler, "session-default", "teleport", authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown transport command") {
		t.Fatalf("unknown command errors = %#v, want unknown transport command", errs)
	}
}

// TestControlSessionRejectsCrossProfile is the AV-3 hard same-Profile gate:
// a DIFFERENT-Profile device can neither control nor learn of
// another Profile's session — the target is reported "unknown session", with no
// existence leak, and no outbox row is written.
//
// feature:cross-device-session-control
func TestControlSessionRejectsCrossProfile(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// session-default (profile-default) is playing media-alpha.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	// The managed-Profile caller tries to control it — rejected as unknown.
	_, errs := controlSessionPost(t, handler, "session-default", "pause", authorizeManaged)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "unknown session") {
		t.Fatalf("cross-Profile control errors = %#v, want unknown session", errs)
	}

	// No session-control event was published (the rejection is before the write).
	events, err := st.ReadEventOutbox(t.Context(), 0, 256)
	if err != nil {
		t.Fatalf("ReadEventOutbox: %v", err)
	}
	for _, e := range events {
		if e.AggregateType == store.OutboxAggregateSession {
			t.Fatalf("cross-Profile control leaked an outbox event: %#v", e)
		}
	}
}

// TestControlSessionRevealGate is the AV-3 transport Reveal acceptance
// (ADR-0035 §"Security surface"): an unrevealed same-Profile device cannot
// control a session playing restricted Media it has not revealed — the target
// is reported "session not controllable" without naming the Media, and a
// revealed same-Profile device can.
//
// feature:cross-device-session-control
func TestControlSessionRevealGate(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	// session-default (profile-default) is playing restricted media-archive.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", true)

	// Unrevealed same-Profile caller (session-manager) is refused without a leak.
	_, errs := controlSessionPost(t, handler, "session-default", "pause", authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not controllable") {
		t.Fatalf("unrevealed control errors = %#v, want not controllable", errs)
	}
	if strings.Contains(errsJoin(errs), "media-archive") {
		t.Fatalf("control error leaked the restricted media id: %#v", errs)
	}

	// A visible row for the same session does not unlock it: one hidden row
	// makes the whole session uncontrollable, whichever row sorts first.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)
	for _, command := range []string{"pause", "play-media"} {
		vars := map[string]any{"sessionID": "session-default", "command": command}
		if command == "play-media" {
			vars["mediaID"] = "media-alpha"
		}
		_, errs = controlSessionVars(t, handler, vars, authorizeManager)
		if len(errs) == 0 || !strings.Contains(errs[0].Message, "not controllable") {
			t.Fatalf("unrevealed %s with a mixed-visibility session errors = %#v, want not controllable", command, errs)
		}
	}

	// A revealed same-Profile caller (session-revealed) can control it.
	data, errs := controlSessionPost(t, handler, "session-default", "pause", authorizeRevealed)
	if len(errs) != 0 {
		t.Fatalf("revealed control errors = %#v", errs)
	}
	var result ControlSessionResult
	if err := json.Unmarshal(data["controlSession"], &result); err != nil {
		t.Fatalf("decode revealed controlSession: %v", err)
	}
	if result.SessionID != "session-default" {
		t.Errorf("revealed control echo = %q, want session-default", result.SessionID)
	}

	// The transport command carries every Media the session holds a token for
	// as the receiver's anchor set; a play-media replacing it carries none.
	if _, errs := controlSessionVars(t, handler, map[string]any{
		"sessionID": "session-default", "command": "play-media", "mediaID": "media-alpha",
	}, authorizeRevealed); len(errs) != 0 {
		t.Fatalf("revealed play-media errors = %#v", errs)
	}
	payloads := sessionControlPayloads(t, st)
	if len(payloads) != 2 {
		t.Fatalf("outbox payloads = %d, want 2", len(payloads))
	}
	playing := map[store.MediaID]bool{}
	for _, id := range payloads[0].PlayingMediaIDs {
		playing[id] = true
	}
	if len(payloads[0].PlayingMediaIDs) != 2 || !playing["media-alpha"] || !playing["media-archive"] {
		t.Errorf("pause playingMediaIDs = %v, want media-alpha and media-archive", payloads[0].PlayingMediaIDs)
	}
	if payloads[1].Command != store.SessionCommandPlayMedia || len(payloads[1].PlayingMediaIDs) != 0 {
		t.Errorf("play-media payload = %#v, want no playingMediaIDs", payloads[1])
	}
}

// TestProfileSessionsExcludeIdlePlaybacksButControlReachesThem covers
// CORE-2346: profileSessions applies the same 2-minute activity window as
// Console liveSessions, while controlSession still reaches (and reveal-guards)
// a paused playback through GrantedSessions.
func TestProfileSessionsExcludeIdlePlaybacksButControlReachesThem(t *testing.T) {
	dataDir := storetest.MigratedDataDir(t)
	st := storetest.OpenAt(t, dataDir)
	handler := newTestHandlerWithStore(st)

	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-alpha", false)

	idleSince := time.Now().Add(-store.LiveSessionActivityWindow - time.Minute)
	storetest.BackdateStreamTokenActivity(t, dataDir, "session-default", idleSince)

	data, errs := post(t, handler, `{"query":"query { profileSessions { sessionID } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("profileSessions errors = %#v", errs)
	}
	var got []ProfileSessionInfo
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode profileSessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("profileSessions for idle playback = %#v, want none", got)
	}

	if _, errs := controlSessionPost(t, handler, "session-default", "pause", authorizeManager); len(errs) != 0 {
		t.Fatalf("controlSession on paused playback errors = %#v", errs)
	}

	// A progress report restores profileSessions.
	_, errs = post(t, handler, `{"query":"mutation { reportPlayback(mediaID: \"media-alpha\", positionSeconds: 15, durationSeconds: 120) { mediaID } }"}`, authorizeViewer)
	if len(errs) != 0 {
		t.Fatalf("reportPlayback errors = %#v", errs)
	}
	data, errs = post(t, handler, `{"query":"query { profileSessions { sessionID } }"}`, authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("profileSessions after progress errors = %#v", errs)
	}
	if err := json.Unmarshal(data["profileSessions"], &got); err != nil {
		t.Fatalf("decode profileSessions after progress: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "session-default" {
		t.Fatalf("profileSessions after progress = %#v, want session-default", got)
	}
	if _, errs := controlSessionPost(t, handler, "session-default", "pause", authorizeManager); len(errs) != 0 {
		t.Fatalf("controlSession after progress errors = %#v", errs)
	}
}

func errsJoin(errs []graphError) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Message
	}
	return strings.Join(parts, " | ")
}
