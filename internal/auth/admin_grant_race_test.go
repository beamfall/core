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
