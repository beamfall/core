package store

import (
	"context"
	"testing"
	"time"
)

// TestSignedInDeviceIDsDropsSignedOutDevices covers CORE-1607: a device whose
// session was terminated (sign-out) is no longer signed in, though it is not
// revoked; an expired session still counts because the device can renew it.
func TestSignedInDeviceIDsDropsSignedOutDevices(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	for _, d := range []struct {
		device, session string
		expires         time.Time
	}{
		{"device-in", "session-in", now.Add(time.Hour)},
		{"device-lapsed", "session-lapsed", now.Add(-time.Hour)},
		{"device-out", "session-out", now.Add(time.Hour)},
	} {
		if err := st.EnrollDevice(ctx,
			Device{ID: DeviceID(d.device), Name: "Browser", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("s")},
			Session{ID: SessionID(d.session), DeviceID: DeviceID(d.device), ProfileID: "profile-default", ExpiresAt: d.expires},
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TerminateSession(ctx, "session-out"); err != nil {
		t.Fatal(err)
	}
	signedIn, err := st.SignedInDeviceIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !signedIn["device-in"] || !signedIn["device-lapsed"] || signedIn["device-out"] {
		t.Fatalf("signed-in devices = %v, want device-in and device-lapsed only", signedIn)
	}
}
