package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeTerminator records the SessionIDs revokeDevice asked it to abort and
// returns a configured per-session abort count, standing in for *playback.Sessions.
type fakeTerminator struct {
	calls   []store.SessionID
	perCall int
}

func (f *fakeTerminator) TerminateSession(sessionID store.SessionID) int {
	f.calls = append(f.calls, sessionID)
	return f.perCall
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
	term := &fakeTerminator{perCall: 1}
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

	// The terminator was called for exactly device-tv's two sessions — never
	// device-mac's. This is the "other devices' sessions are unaffected" invariant.
	wantSessions := map[store.SessionID]bool{"session-default": true, "session-managed": true}
	if len(term.calls) != 2 {
		t.Fatalf("terminator calls = %v, want exactly the two device-tv sessions", term.calls)
	}
	for _, s := range term.calls {
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
	handler.SetTranscodeTerminator(&fakeTerminator{perCall: 1})

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
