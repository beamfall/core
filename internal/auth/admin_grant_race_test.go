package auth

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestBeginAdminSessionLosesRaceWithOwnershipTransfer reproduces the CORE-0728
// ordering: the caller passes the owner check, an ownership transfer commits,
// and only then would the Admin Session window be written. BeginAdminSession
// must deny and leave the former owner's session without an admin window.
// feature:admin-session
func TestBeginAdminSessionLosesRaceWithOwnershipTransfer(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_600_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	request := authenticatedRequest("session-manager", "device-mac", "dev-manager-secret")
	scope, err := service.ScopeFromRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other Owner", store.ProfileAdult,
		store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}

	service.beforeAdminGrant = func() {
		if err := st.SetProfileOwner(ctx, other.ID); err != nil {
			t.Errorf("transfer: %v", err)
		}
	}
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != ErrAdminPassword {
		t.Fatalf("BeginAdminSession err = %v, want ErrAdminPassword after losing the race", err)
	}
	after, err := service.ScopeFromRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if after.Admin || !after.AdminUntil.IsZero() {
		t.Fatalf("former owner holds an Admin Session after the transfer: %#v", after)
	}
}

// TestAdminReauthLimitSurvivesSessionRenewal: renewal mints a new session ID,
// so a session-keyed lane would hand a holder of a Manager browser's device
// credential a fresh guess budget per renewal. The lane is keyed by Profile,
// so the attempt after the budget is still throttled from the renewed session.
// feature:admin-session
func TestAdminReauthLimitSurvivesSessionRenewal(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_700_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope, err := service.ScopeFromRequest(ctx, authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < adminBackoff.FreeAttempts; i++ {
		if _, err := service.BeginAdminSession(ctx, scope, "wrong"); err != ErrAdminPassword {
			t.Fatalf("attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	if _, err := st.RenewDeviceSession(ctx, scope.DeviceID, "session-renewed", now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	renewed, err := service.ScopeFromRequest(ctx, authenticatedRequest("session-renewed", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginAdminSession(ctx, renewed, "right-password"); err != ErrAdminLocked {
		t.Fatalf("BeginAdminSession after renewal err = %v, want ErrAdminLocked", err)
	}
	if _, err := service.ChangeSignInSecurityMode(ctx, renewed, store.SignInSecurityStrict, "right-password"); err != ErrAdminLocked {
		t.Fatalf("ChangeSignInSecurityMode after renewal err = %v, want ErrAdminLocked", err)
	}
}

// TestChangeSignInSecurityModeLosesRaceWithOwnershipTransfer: the caller
// passes the owner and password checks, an ownership transfer commits, and
// only then would the mode be written. The store re-checks ownership in the
// write's transaction, so the switch is denied and the mode is unchanged.
// feature:admin-session
func TestChangeSignInSecurityModeLosesRaceWithOwnershipTransfer(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(2_900_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope, err := service.ScopeFromRequest(ctx, authenticatedRequest("session-manager", "device-mac", "dev-manager-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, scope.ProfileID); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProfile(ctx, "Other Owner", store.ProfileAdult,
		store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	service.beforeModeWrite = func() {
		if err := st.SetProfileOwner(ctx, other.ID); err != nil {
			t.Errorf("transfer: %v", err)
		}
	}
	if _, err := service.ChangeSignInSecurityMode(ctx, scope, store.SignInSecurityStrict, "right-password"); err != ErrAdminPassword {
		t.Fatalf("ChangeSignInSecurityMode err = %v, want ErrAdminPassword after losing the race", err)
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != store.SignInSecurityHome {
		t.Fatalf("stored mode = (%q, %v), want home", mode, err)
	}
	if audit := signInSecurityAudit(t, st); len(audit) != 0 {
		t.Fatalf("lost race audited as a change: %+v", audit)
	}
}
