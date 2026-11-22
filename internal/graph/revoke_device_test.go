package graph

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeTerminator stands in for *playback.Sessions. live holds how many
// transcodes each session runs; TerminateSession stops and reports them, and a
// session with none is the zero-stop no-op the real manager returns. aborted
// records, in call order, the sessions that had something to stop, and
// playbacks records every DetachPlayback call.
type fakeTerminator struct {
	live      map[store.SessionID]int
	aborted   []store.SessionID
	playbacks []fakePlayback
}

type fakePlayback struct {
	sessionID store.SessionID
	mediaID   store.MediaID
}

func (f *fakeTerminator) TerminateSession(sessionID store.SessionID) int {
	n := f.live[sessionID]
	if n > 0 {
		delete(f.live, sessionID)
		f.aborted = append(f.aborted, sessionID)
	}
	return n
}

// DetachPlayback records the call. It does not change live, which lets a
// test model a transcode still running after endPlayback (a late segment
// request restarted it, or a pre-CORE-2345 server never stopped it).
func (f *fakeTerminator) DetachPlayback(sessionID store.SessionID, mediaID store.MediaID) (int, func()) {
	f.playbacks = append(f.playbacks, fakePlayback{sessionID: sessionID, mediaID: mediaID})
	return 0, func() {}
}

// TestRevokeDeviceAbortsTranscodesForThatDeviceOnly is the SEC-4 transcode-abort
// proof: revoking a device key (the audited server mutation) flips
// the Device's Revoked flag AND aborts every in-flight transcode owned by that
// device's active Sessions through the playback termination seam — while another
// device's transcode session is left untouched. The event-stream / deviceBus half
// (a revoked device stops receiving subscription events while an unrevoked one
// keeps streaming) is proven in the server package
// (TestEventStreamTerminatesOnDeviceRevocation).
//
// feature:device-revocation
func TestRevokeDeviceAbortsTranscodesForThatDeviceOnly(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	term := &fakeTerminator{live: map[store.SessionID]int{"session-default": 1, "session-managed": 1, "session-manager": 1}}
	handler.SetTranscodeTerminator(term)
	openManagerAdminSession(t, st)

	// device-tv (the target) holds two active playback sessions; device-mac
	// (a different device) holds one. Only device-tv's must be aborted.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-manager", "profile-default", "device-mac", "media-hls", "media-alpha", false)

	data, errs := post(t, handler,
		`{"query":"mutation R($deviceID: String!){ revokeDevice(deviceID:$deviceID){ deviceID transcodesAborted } }","variables":{"deviceID":"device-tv"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("revokeDevice errors = %#v", errs)
	}
	var got RevokeDeviceResult
	if err := json.Unmarshal(data["revokeDevice"], &got); err != nil {
		t.Fatalf("decode revokeDevice: %v", err)
	}
	if got.DeviceID != "device-tv" {
		t.Fatalf("revoked device = %q, want device-tv", got.DeviceID)
	}
	// Two of device-tv's sessions were terminated (one abort each via the fake).
	if got.TranscodesAborted != 2 {
		t.Errorf("transcodesAborted = %d, want 2", got.TranscodesAborted)
	}

	// Exactly device-tv's two transcodes were aborted — never device-mac's. This
	// is the "other devices' sessions are unaffected" invariant.
	wantSessions := map[store.SessionID]bool{"session-default": true, "session-managed": true}
	if len(term.aborted) != 2 {
		t.Fatalf("aborted sessions = %v, want exactly the two device-tv sessions", term.aborted)
	}
	if term.live["session-manager"] != 1 {
		t.Errorf("device-mac's transcode was stopped; live = %v", term.live)
	}
	for _, s := range term.aborted {
		if !wantSessions[s] {
			t.Errorf("terminator called for unexpected session %q (device-mac must be untouched)", s)
		}
	}

	// The device is now revoked in the store — future auth is blocked too.
	dev, err := st.Device(t.Context(), "device-tv")
	if err != nil {
		t.Fatalf("read device: %v", err)
	}
	if !dev.Revoked {
		t.Errorf("device-tv Revoked = false after revokeDevice, want true")
	}

	// The mutation is audited (revocation routes through a server mutation).
	entries := allActivity(t, st)
	found := false
	for _, e := range entries {
		if e.Action == store.ActivityDeviceRevoke && e.TargetType == "device" && e.TargetID == "device-tv" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no %q activity entry for device-tv: %#v", store.ActivityDeviceRevoke, entries)
	}
}

// TestRevokeDeviceGuards covers the gate and error edges: a non-admin caller is
// refused, an unknown device id is a client error (not a false success), and a
// revoked device with no live transcode aborts zero (event-stream termination is
// the whole propagation for that case).
//
// feature:device-revocation
func TestRevokeDeviceGuards(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetTranscodeTerminator(&fakeTerminator{live: map[store.SessionID]int{}})

	// Admin-Session gate: a manager session without an open Admin Session is refused.
	_, errs := post(t, handler,
		`{"query":"mutation R($deviceID: String!){ revokeDevice(deviceID:$deviceID){ deviceID } }","variables":{"deviceID":"device-tv"}}`,
		authorizeManager)
	if len(errs) == 0 || errs[0].Message != "admin session required" {
		t.Fatalf("non-admin revokeDevice errors = %#v, want admin-session gate", errs)
	}

	openManagerAdminSession(t, st)

	// Unknown device id is a client error.
	_, errs = post(t, handler,
		`{"query":"mutation R($deviceID: String!){ revokeDevice(deviceID:$deviceID){ deviceID } }","variables":{"deviceID":"nope"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "not found") {
		t.Fatalf("revoke unknown errors = %#v, want device not found", errs)
	}

	// A device with no live transcode aborts zero transcodes but still revokes.
	data, errs := post(t, handler,
		`{"query":"mutation R($deviceID: String!){ revokeDevice(deviceID:$deviceID){ deviceID transcodesAborted } }","variables":{"deviceID":"device-tv"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("revokeDevice (no transcode) errors = %#v", errs)
	}
	var got RevokeDeviceResult
	if err := json.Unmarshal(data["revokeDevice"], &got); err != nil {
		t.Fatalf("decode revokeDevice: %v", err)
	}
	if got.TranscodesAborted != 0 {
		t.Errorf("transcodesAborted = %d for device with no live transcode, want 0", got.TranscodesAborted)
	}
}

func TestSetDeviceRoleGuardsCurrentDevice(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"mutation R($deviceID: String!, $role: String!){ setDeviceRole(deviceID:$deviceID, role:$role){ deviceID role current } }","variables":{"deviceID":"device-tv","role":"manager"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("setDeviceRole errors = %#v", errs)
	}
	var got DeviceInfo
	if err := json.Unmarshal(data["setDeviceRole"], &got); err != nil {
		t.Fatalf("decode setDeviceRole: %v", err)
	}
	if got.Role != "manager" || got.Current {
		t.Fatalf("setDeviceRole = %+v, want manager non-current", got)
	}

	_, errs = post(t, handler,
		`{"query":"mutation R($deviceID: String!, $role: String!){ setDeviceRole(deviceID:$deviceID, role:$role){ deviceID } }","variables":{"deviceID":"device-mac","role":"viewer"}}`,
		authorizeManager)
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "current device") {
		t.Fatalf("current device role change errors = %#v, want current-device guard", errs)
	}
}

// TestRevokeDeviceAbortsATranscodeAfterEndPlayback is the CORE-2345 proof.
// endPlayback asks the terminator to stop the caller's transcode of that Media,
// and it clears the session's stream-token ledger rows. Revocation used to find
// a device's transcodes only through that ledger, so a transcode still running
// afterwards (here one the fake keeps alive, as a late segment request would)
// was never aborted. It must now be found through the device's auth sessions.
//
// feature:device-revocation
func TestRevokeDeviceAbortsATranscodeAfterEndPlayback(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	term := &fakeTerminator{live: map[store.SessionID]int{"session-default": 1}}
	handler.SetTranscodeTerminator(term)
	openManagerAdminSession(t, st)

	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false)
	data, errs := post(t, handler, `{"query":"mutation { endPlayback(mediaId: \"media-alpha\") }"}`, authorizeViewer)
	if len(errs) != 0 || string(data["endPlayback"]) != "true" {
		t.Fatalf("endPlayback = %s, errors %#v", data["endPlayback"], errs)
	}
	want := []fakePlayback{{sessionID: "session-default", mediaID: "media-alpha"}}
	if !slices.Equal(term.playbacks, want) {
		t.Fatalf("TerminatePlayback calls = %v, want %v", term.playbacks, want)
	}
	if got := liveSessionIDs(t, handler); len(got) != 0 {
		t.Fatalf("live sessions after endPlayback = %v, want none (ledger cleared)", got)
	}

	data, errs = post(t, handler,
		`{"query":"mutation R($deviceID: String!){ revokeDevice(deviceID:$deviceID){ deviceID transcodesAborted } }","variables":{"deviceID":"device-tv"}}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("revokeDevice errors = %#v", errs)
	}
	var got RevokeDeviceResult
	if err := json.Unmarshal(data["revokeDevice"], &got); err != nil {
		t.Fatalf("decode revokeDevice: %v", err)
	}
	if got.TranscodesAborted != 1 {
		t.Errorf("transcodesAborted = %d, want 1 (the transcode outlived its ledger rows)", got.TranscodesAborted)
	}
	if !slices.Equal(term.aborted, []store.SessionID{"session-default"}) {
		t.Errorf("aborted sessions = %v, want [session-default]", term.aborted)
	}
}
