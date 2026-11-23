package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSignInSecurityModeDefaultsToHome(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != SignInSecurityHome {
		t.Fatalf("SignInSecurityMode on a fresh store = (%q, %v), want home", mode, err)
	}
}

func TestBootstrapOwnerStoresSignInSecurityMode(t *testing.T) {
	for _, tc := range []struct {
		in   SignInSecurityMode
		want SignInSecurityMode
	}{{"", SignInSecurityHome}, {SignInSecurityHome, SignInSecurityHome}, {SignInSecurityStrict, SignInSecurityStrict}} {
		t.Run(string(tc.want)+"/"+string(tc.in), func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(ctx, migratedDataDir(t), Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			device, session := bootstrapEnrollment("mode")
			if _, err := st.BootstrapOwner(ctx, "Owner", device, session, tc.in); err != nil {
				t.Fatalf("BootstrapOwner: %v", err)
			}
			if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != tc.want {
				t.Fatalf("SignInSecurityMode = (%q, %v), want %q", mode, err, tc.want)
			}
		})
	}
}

func TestBootstrapOwnerRejectsUnknownSignInSecurityMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	device, session := bootstrapEnrollment("bad")
	_, err = st.BootstrapOwner(ctx, "Owner", device, session, "paranoid")
	var verr ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("BootstrapOwner(paranoid) = %v, want ValidationError", err)
	}
	if n := countRows(t, st, "profile"); n != 0 {
		t.Fatalf("profile rows after refused setup = %d, want 0", n)
	}
}

func TestBootstrapOwnerFailureStoresNoSignInSecurityMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.catalogW.ExecContext(ctx, `
		CREATE TRIGGER inject_enroll_failure BEFORE INSERT ON auth_session
		BEGIN SELECT RAISE(ABORT, 'injected enroll failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	device, session := bootstrapEnrollment("fail")
	if _, err := st.BootstrapOwner(ctx, "Owner", device, session, SignInSecurityStrict); err == nil {
		t.Fatal("BootstrapOwner succeeded through an injected session failure")
	}
	var n int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT COUNT(*) FROM server_config WHERE key = ?`, configSignInSecurityMode).Scan(&n); err != nil || n != 0 {
		t.Fatalf("mode rows after failed setup = (%d, %v), want 0", n, err)
	}
}

func TestSetSignInSecurityModeReturnsOldMode(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Unix(1_800_000_000, 0)
	old, err := st.SetSignInSecurityMode(ctx, SignInSecurityStrict, now)
	if err != nil || old != SignInSecurityHome {
		t.Fatalf("SetSignInSecurityMode(strict) = (%q, %v), want old home", old, err)
	}
	old, err = st.SetSignInSecurityMode(ctx, SignInSecurityHome, now)
	if err != nil || old != SignInSecurityStrict {
		t.Fatalf("SetSignInSecurityMode(home) = (%q, %v), want old strict", old, err)
	}
	var verr ValidationError
	if _, err := st.SetSignInSecurityMode(ctx, "open", now); !errors.As(err, &verr) {
		t.Fatalf("SetSignInSecurityMode(open) = %v, want ValidationError", err)
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != SignInSecurityHome {
		t.Fatalf("mode after refused write = (%q, %v), want home", mode, err)
	}
}

func TestSetSignInSecurityModeAsOwnerRechecksTheCaller(t *testing.T) {
	ctx := context.Background()
	st := openBackoffStore(t)
	now := time.Unix(1_000_000, 0).UTC()
	manager, err := st.Session(ctx, "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	set := func(session SessionID, profile ProfileID) bool {
		t.Helper()
		_, ok, err := st.SetSignInSecurityModeAsOwner(ctx, session, profile, SignInSecurityStrict, now)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if set("session-default", manager.ProfileID) {
		t.Fatal("a session bound to another device or Profile changed the mode")
	}
	if err := st.SetProfileOwner(ctx, manager.ProfileID); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other Owner", ProfileAdult, LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if set("session-manager", manager.ProfileID) {
		t.Fatal("a former owner changed the mode")
	}
	if err := st.SetProfileOwner(ctx, manager.ProfileID); err != nil {
		t.Fatal(err)
	}
	if !set("session-manager", manager.ProfileID) {
		t.Fatal("the owner's Manager session could not change the mode")
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != SignInSecurityStrict {
		t.Fatalf("stored mode = (%q, %v), want strict", mode, err)
	}
}

func TestEnrollAdminSignInDeviceRereadsModeAndOwner(t *testing.T) {
	ctx := context.Background()
	st := openBackoffStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	device := func(id DeviceID) Device {
		return Device{ID: id, Name: "Laptop", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")}
	}
	expires := now.Add(DefaultSessionLifetimeDays * 24 * time.Hour)
	adminUntil := now.Add(DefaultAdminSessionMinutes * time.Minute)
	if err := st.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	stamp, err := st.AdminCredentialStamp(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.EnrollAdminSignInDevice(ctx, device("device-a"), "session-a", now, stamp); !errors.Is(err, ErrNoOwnerProfile) {
		t.Fatalf("no owner err = %v, want ErrNoOwnerProfile", err)
	}
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSignInSecurityMode(ctx, SignInSecurityStrict, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnrollAdminSignInDevice(ctx, device("device-a"), "session-a", now, stamp); !errors.Is(err, ErrAdminSignInOff) {
		t.Fatalf("strict err = %v, want ErrAdminSignInOff", err)
	}
	if _, err := st.Device(ctx, "device-a"); err == nil {
		t.Fatal("strict mode enrolled a device")
	}
	if _, err := st.SetSignInSecurityMode(ctx, SignInSecurityHome, now); err != nil {
		t.Fatal(err)
	}
	session, err := st.EnrollAdminSignInDevice(ctx, device("device-a"), "session-a", now, stamp)
	if err != nil || session.ProfileID != "profile-default" || session.Kind != SessionKindEnrolled || !session.ExpiresAt.Equal(expires) || !session.AdminUntil.Equal(adminUntil) {
		t.Fatalf("home enroll = %+v, %v", session, err)
	}
	if got, err := st.Session(ctx, "session-a"); err != nil || got.DeviceID != "device-a" || !got.ExpiresAt.Equal(expires) || !got.AdminUntil.Equal(adminUntil) {
		t.Fatalf("stored session = %+v, %v", got, err)
	}

	never := 0
	if _, err := st.SetConsoleConfig(ctx, ConsoleConfigPatch{SessionLifetimeDays: &never, AdminSessionMinutes: &never}, now); err != nil {
		t.Fatal(err)
	}
	neverSession, err := st.EnrollAdminSignInDevice(ctx, device("device-b"), "session-b", now, stamp)
	if err != nil || !neverSession.ExpiresAt.Equal(SessionNeverExpires) || !neverSession.AdminUntil.Equal(SessionNeverExpires) {
		t.Fatalf("never-expire enroll = %+v, %v", neverSession, err)
	}
}

// CORE-1157: an admin password reset that lands after sign-in verified the old
// password, but before enrollment commits, must refuse the enrollment instead
// of opening a fresh Admin Session with the old credential.
func TestEnrollAdminSignInDeviceRefusesAfterPasswordReset(t *testing.T) {
	ctx := context.Background()
	st := openBackoffStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "old password", now); err != nil {
		t.Fatal(err)
	}
	stamp, err := st.AdminCredentialStamp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyAdminPassword(ctx, "old password"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "new password", now); err != nil {
		t.Fatal(err)
	}
	dev := Device{ID: "device-a", Name: "Laptop", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")}
	if _, err := st.EnrollAdminSignInDevice(ctx, dev, "session-a", now, stamp); !errors.Is(err, ErrAdminPasswordMismatch) {
		t.Fatalf("enroll after reset err = %v, want ErrAdminPasswordMismatch", err)
	}
	if _, err := st.Device(ctx, "device-a"); err == nil {
		t.Fatal("enrollment after a password reset inserted a device")
	}
}
