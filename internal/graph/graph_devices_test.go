package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Device administration, graph tests. The seed enrolls two devices —
// device-tv (TV, viewer) and device-mac (Manager, manager). authorizeManager
// requests come from device-mac, so that is the "current" device in the list.

func TestDevicesQueryListsEnrolledDevicesWithCurrentFlag(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { devices { deviceID name role revoked current } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("devices errors = %#v", errs)
	}
	var got []DeviceInfo
	if err := json.Unmarshal(data["devices"], &got); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	// The seed enrolls device-tv (viewer), device-mac (manager), and
	// device-revoked (viewer, already revoked).
	if len(got) != 3 {
		t.Fatalf("devices = %d, want 3 (seeded device-tv + device-mac + device-revoked)", len(got))
	}
	byID := map[string]DeviceInfo{}
	for _, d := range got {
		byID[d.DeviceID] = d
	}
	mac, ok := byID["device-mac"]
	if !ok {
		t.Fatal("device-mac missing from list")
	}
	if !mac.Current {
		t.Error("device-mac (the requesting device) must be marked current")
	}
	if mac.Role != "manager" {
		t.Errorf("device-mac role = %q, want manager", mac.Role)
	}
	tv, ok := byID["device-tv"]
	if !ok {
		t.Fatal("device-tv missing from list")
	}
	if tv.Current {
		t.Error("device-tv must NOT be marked current")
	}
	if tv.Role != "viewer" {
		t.Errorf("device-tv role = %q, want viewer", tv.Role)
	}
	revoked, ok := byID["device-revoked"]
	if !ok {
		t.Fatal("device-revoked missing from list")
	}
	if !revoked.Revoked {
		t.Error("device-revoked must be marked revoked")
	}
}

func TestDevicesQueryNeverSerializesSecretHash(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)

	data, errs := post(t, handler,
		`{"query":"query { devices { deviceID name role revoked current } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("devices errors = %#v", errs)
	}
	// The device secret hash is a credential — it must never appear on the wire.
	if strings.Contains(strings.ToLower(string(data["devices"])), "secret") ||
		strings.Contains(string(data["devices"]), "Hash") {
		t.Errorf("devices payload leaked a secret/hash field: %s", data["devices"])
	}
}

func TestDevicesQueryRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	// No openManagerAdminSession: a manager without an open Admin Session must be
	// refused, like every other Console operation.

	_, errs := post(t, handler,
		`{"query":"query { devices { deviceID } }"}`,
		authorizeManager)
	if len(errs) == 0 {
		t.Fatal("devices without an Admin Session must error")
	}
}

func TestRevokeAllDevicesRevokesOthersNotSelf(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	term := &fakeTerminator{perCall: 1}
	handler.SetTranscodeTerminator(term)
	openManagerAdminSession(t, st)

	for _, fixture := range []struct {
		device  store.Device
		session store.Session
	}{
		{
			device:  store.Device{ID: "device-lounge", Name: "Lounge", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("lounge-secret")},
			session: store.Session{ID: "session-lounge", DeviceID: "device-lounge", ProfileID: "profile-default"},
		},
		{
			device:  store.Device{ID: "device-empty", Name: "Empty", Role: store.DeviceRoleViewer, SecretHash: store.HashDeviceSecret("empty-secret")},
			session: store.Session{ID: "session-empty", DeviceID: "device-empty", ProfileID: "profile-default"},
		},
	} {
		if err := st.EnrollDevice(t.Context(), fixture.device, fixture.session); err != nil {
			t.Fatal(err)
		}
	}

	// Three eligible devices are interleaved with the current and already-revoked
	// devices. device-empty has no active session; session-default produces two
	// ActiveSession rows and must still be terminated once.
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-lounge", "profile-default", "device-lounge", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-default", "profile-default", "device-tv", "media-direct", "media-archive", false)
	recordActive(t, st, "session-manager", "profile-default", "device-mac", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-managed", "profile-managed", "device-tv", "media-hls", "media-alpha", false)
	recordActive(t, st, "session-revoked", "profile-default", "device-revoked", "media-hls", "media-alpha", false)

	data, errs := post(t, handler,
		`{"query":"mutation { revokeAllDevices { revokedCount transcodesAborted } }"}`,
		authorizeManager)
	if len(errs) != 0 {
		t.Fatalf("revokeAllDevices errors = %#v", errs)
	}
	var got RevokeAllDevicesResult
	if err := json.Unmarshal(data["revokeAllDevices"], &got); err != nil {
		t.Fatalf("decode revokeAllDevices: %v", err)
	}
	if got.RevokedCount != 3 {
		t.Errorf("revokedCount = %d, want 3", got.RevokedCount)
	}
	if got.TranscodesAborted != 3 {
		t.Errorf("transcodesAborted = %d, want 3", got.TranscodesAborted)
	}
	wantCalls := []store.SessionID{"session-lounge", "session-default", "session-managed"}
	if len(term.calls) != len(wantCalls) {
		t.Fatalf("terminator calls = %v, want %v", term.calls, wantCalls)
	}
	for i := range wantCalls {
		if term.calls[i] != wantCalls[i] {
			t.Fatalf("terminator calls = %v, want %v", term.calls, wantCalls)
		}
	}

	// device-mac (self) must remain unrevoked; device-tv must be revoked.
	mac, err := st.Device(t.Context(), "device-mac")
	if err != nil {
		t.Fatalf("Device(device-mac): %v", err)
	}
	if mac.Revoked {
		t.Error("device-mac (the requesting device) must NOT be revoked by revoke-all")
	}
	tv, err := st.Device(t.Context(), "device-tv")
	if err != nil {
		t.Fatalf("Device(device-tv): %v", err)
	}
	if !tv.Revoked {
		t.Error("device-tv must be revoked by revoke-all")
	}
	revoked, err := st.Device(t.Context(), "device-revoked")
	if err != nil {
		t.Fatalf("Device(device-revoked): %v", err)
	}
	if !revoked.Revoked {
		t.Error("device-revoked must remain revoked")
	}

	var auditTargets []string
	for _, entry := range allActivity(t, st) {
		if entry.Action == store.ActivityDeviceRevoke {
			auditTargets = append(auditTargets, entry.TargetID)
		}
	}
	wantAuditTargets := []string{"device-tv", "device-lounge", "device-empty"}
	if len(auditTargets) != len(wantAuditTargets) {
		t.Fatalf("revoke audit targets = %v, want %v", auditTargets, wantAuditTargets)
	}
	for i := range wantAuditTargets {
		if auditTargets[i] != wantAuditTargets[i] {
			t.Fatalf("revoke audit targets = %v, want %v", auditTargets, wantAuditTargets)
		}
	}
}

func TestListDevicesStoreReturnsEnrolledDevices(t *testing.T) {
	st := storetest.Open(t)
	devices, err := st.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	ids := map[store.DeviceID]bool{}
	for _, d := range devices {
		ids[d.ID] = true
	}
	if !ids["device-tv"] || !ids["device-mac"] {
		t.Errorf("ListDevices missing seeded devices, got %v", ids)
	}
}
