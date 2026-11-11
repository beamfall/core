package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRenewDeviceSessionRotatesInOneWrite(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-renew", Name: "Browser", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-old", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(-time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	latest, err := st.LatestDeviceSession(ctx, "device-renew")
	if err != nil || latest.ID != "session-old" || latest.ProfileID != "profile-default" {
		t.Fatalf("LatestDeviceSession = %+v, %v", latest, err)
	}

	next := Session{ID: "session-new", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if err := st.RenewDeviceSession(ctx, "session-old", next); err != nil {
		t.Fatalf("RenewDeviceSession: %v", err)
	}
	if _, err := st.Session(ctx, "session-old"); err == nil {
		t.Fatal("old session survived renewal")
	}
	got, err := st.Session(ctx, "session-new")
	if err != nil || got.DeviceID != "device-renew" || got.Kind != SessionKindEnrolled || got.Revealed || !got.AdminUntil.IsZero() {
		t.Fatalf("new session = %+v, %v", got, err)
	}
	latest, err = st.LatestDeviceSession(ctx, "device-renew")
	if err != nil || latest.ID != "session-new" {
		t.Fatalf("LatestDeviceSession after renew = %+v, %v", latest, err)
	}

	// An old session that belongs to another device is left alone.
	other := Session{ID: "session-third", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(31 * 24 * time.Hour)}
	if err := st.RenewDeviceSession(ctx, "session-manager", other); err != nil {
		t.Fatalf("RenewDeviceSession with foreign old session: %v", err)
	}
	if _, err := st.Session(ctx, "session-manager"); err != nil {
		t.Fatalf("renewal deleted another device's session: %v", err)
	}
}

func TestRenewDeviceSessionRefusals(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	if _, err := st.LatestDeviceSession(ctx, "device-unknown"); !errors.Is(err, ErrNoDeviceSession) {
		t.Fatalf("LatestDeviceSession unknown device = %v, want ErrNoDeviceSession", err)
	}
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-gone", Name: "Browser", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-gone", DeviceID: "device-gone", ProfileID: "profile-default", ExpiresAt: now},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeDevice(ctx, "device-gone"); err != nil {
		t.Fatal(err)
	}
	next := Session{ID: "session-x", DeviceID: "device-gone", ProfileID: "profile-default", ExpiresAt: now.Add(time.Hour)}
	if err := st.RenewDeviceSession(ctx, "session-gone", next); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device renewal = %v, want ErrDeviceRevoked", err)
	}
	if _, err := st.Session(ctx, "session-x"); err == nil {
		t.Fatal("revoked device got a session")
	}
	next.DeviceID = "device-unknown"
	if err := st.RenewDeviceSession(ctx, "", next); err == nil {
		t.Fatal("unknown device renewal succeeded")
	}
	next.DeviceID = "device-mac"
	next.ExpiresAt = time.Time{}
	if err := st.RenewDeviceSession(ctx, "", next); err == nil {
		t.Fatal("renewal without expiry succeeded")
	}
}
