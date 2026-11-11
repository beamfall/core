package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func deviceSessionCount(t *testing.T, st *Store, deviceID DeviceID) int {
	t.Helper()
	var n int
	if err := st.catalogR.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM auth_session WHERE device_id = ?`, deviceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRenewDeviceSessionReplacesTheDevicesRow(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-renew", Name: "Browser", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-old", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(-time.Hour)},
	); err != nil {
		t.Fatal(err)
	}

	got, err := st.RenewDeviceSession(ctx, "device-renew", "session-new", now.Add(30*24*time.Hour))
	if err != nil {
		t.Fatalf("RenewDeviceSession: %v", err)
	}
	if got.ID != "session-new" || got.ProfileID != "profile-default" {
		t.Fatalf("renewed session = %+v", got)
	}
	if _, err := st.Session(ctx, "session-old"); err == nil {
		t.Fatal("old session survived renewal")
	}
	stored, err := st.Session(ctx, "session-new")
	if err != nil || stored.DeviceID != "device-renew" || stored.Kind != SessionKindEnrolled || stored.Revealed || !stored.AdminUntil.IsZero() {
		t.Fatalf("new session = %+v, %v", stored, err)
	}
	if n := deviceSessionCount(t, st, "device-renew"); n != 1 {
		t.Fatalf("device session rows = %d, want 1", n)
	}

	// Renewing again replaces the row rather than adding one, and leaves
	// other devices' sessions alone.
	if _, err := st.RenewDeviceSession(ctx, "device-renew", "session-third", now.Add(31*24*time.Hour)); err != nil {
		t.Fatalf("second RenewDeviceSession: %v", err)
	}
	if n := deviceSessionCount(t, st, "device-renew"); n != 1 {
		t.Fatalf("device session rows after second renewal = %d, want 1", n)
	}
	if _, err := st.Session(ctx, "session-new"); err == nil {
		t.Fatal("first renewed session survived the second renewal")
	}
	if _, err := st.Session(ctx, "session-manager"); err != nil {
		t.Fatalf("renewal deleted another device's session: %v", err)
	}
}

func TestRenewDeviceSessionCollapsesExtraRows(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-renew", Name: "Browser", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-a", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx,
		`INSERT INTO auth_session(id, device_id, profile_id, revealed, expires_at_ms, kind) VALUES ('session-b', 'device-renew', 'profile-default', 0, ?, ?)`,
		now.Add(2*time.Hour).UnixMilli(), SessionKindEnrolled,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenewDeviceSession(ctx, "device-renew", "session-c", now.Add(30*24*time.Hour)); err != nil {
		t.Fatalf("RenewDeviceSession: %v", err)
	}
	if n := deviceSessionCount(t, st, "device-renew"); n != 1 {
		t.Fatalf("device session rows = %d, want 1", n)
	}
}

// TestConcurrentRenewDeviceSessionConverges: overlapping renewals of one
// device all return the same session, and the device keeps one row.
func TestConcurrentRenewDeviceSessionConverges(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-renew", Name: "Browser", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-old", DeviceID: "device-renew", ProfileID: "profile-default", ExpiresAt: now.Add(-time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	const tabs = 8
	got := make([]Session, tabs)
	errs := make([]error, tabs)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range tabs {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			got[i], errs[i] = st.RenewDeviceSession(ctx, "device-renew", SessionID(fmt.Sprintf("session-tab-%d", i)), now.Add(30*24*time.Hour))
		}(i)
	}
	start.Done()
	done.Wait()
	for i := range tabs {
		if errs[i] != nil || got[i].ID != got[0].ID {
			t.Fatalf("tab %d = %+v, %v; tab 0 = %+v", i, got[i], errs[i], got[0])
		}
	}
	if n := deviceSessionCount(t, st, "device-renew"); n != 1 {
		t.Fatalf("device session rows = %d, want 1", n)
	}
	if _, err := st.Session(ctx, got[0].ID); err != nil {
		t.Fatalf("converged session missing: %v", err)
	}
	// Past the window, renewal replaces the row again.
	later, err := st.RenewDeviceSession(ctx, "device-renew", "session-later", now.Add(30*24*time.Hour+2*renewConvergeWindow))
	if err != nil || later.ID != "session-later" {
		t.Fatalf("renewal past the window = %+v, %v", later, err)
	}
	if n := deviceSessionCount(t, st, "device-renew"); n != 1 {
		t.Fatalf("device session rows after later renewal = %d, want 1", n)
	}
}

func TestRenewDeviceSessionRefusals(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	now := time.Now().UTC()
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-gone", Name: "Browser", Role: DeviceRoleViewer, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-gone", DeviceID: "device-gone", ProfileID: "profile-default", ExpiresAt: now},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeDevice(ctx, "device-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenewDeviceSession(ctx, "device-gone", "session-x", now.Add(time.Hour)); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("revoked device renewal = %v, want ErrDeviceRevoked", err)
	}
	if _, err := st.Session(ctx, "session-x"); err == nil {
		t.Fatal("revoked device got a session")
	}
	if _, err := st.RenewDeviceSession(ctx, "device-unknown", "session-x", now.Add(time.Hour)); err == nil {
		t.Fatal("unknown device renewal succeeded")
	}
	if _, err := st.RenewDeviceSession(ctx, "device-mac", "session-x", time.Time{}); err == nil {
		t.Fatal("renewal without expiry succeeded")
	}

	// A terminated session leaves the device with no row: renewal is refused
	// and adds nothing.
	if err := st.EnrollDevice(ctx,
		Device{ID: "device-killed", Name: "Browser", Role: DeviceRoleManager, SecretHash: HashDeviceSecret("s")},
		Session{ID: "session-killed", DeviceID: "device-killed", ProfileID: "profile-default", ExpiresAt: now.Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	if err := st.TerminateSession(ctx, "session-killed"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RenewDeviceSession(ctx, "device-killed", "session-x", now.Add(time.Hour)); !errors.Is(err, ErrNoDeviceSession) {
		t.Fatalf("renewal after terminate = %v, want ErrNoDeviceSession", err)
	}
	if n := deviceSessionCount(t, st, "device-killed"); n != 0 {
		t.Fatalf("device session rows after refused renewal = %d, want 0", n)
	}
}
