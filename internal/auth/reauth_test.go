package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestVerifyAdminReauthRequiresFreshPasswordAndRespectsLockout(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	// Even with an active Admin Session, VerifyAdminReauth must still re-verify
	// the password and refuse wrong or empty passwords.
	if _, err := service.BeginAdminSession(ctx, scope, "right-password"); err != nil {
		t.Fatal(err)
	}
	adminScope, err := service.Scope(ctx, scope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !adminScope.Admin {
		t.Fatal("expected active Admin Session")
	}
	if err := service.VerifyAdminReauth(ctx, adminScope, "right-password"); err != nil {
		t.Fatalf("valid VerifyAdminReauth err = %v", err)
	}
	for _, bad := range []string{"wrong-password", ""} {
		if err := service.VerifyAdminReauth(ctx, adminScope, bad); !errors.Is(err, ErrAdminPassword) {
			t.Fatalf("password %q err = %v, want ErrAdminPassword", bad, err)
		}
	}

	// Non-manager devices and API tokens cannot satisfy step-up re-auth.
	viewerScope := adminScope
	viewerScope.DeviceRole = store.DeviceRoleViewer
	if err := service.VerifyAdminReauth(ctx, viewerScope, "right-password"); !errors.Is(err, ErrNotManager) {
		t.Fatalf("viewer VerifyAdminReauth err = %v, want ErrNotManager", err)
	}
	tokenScope := Scope{APITokenID: "tok-1", DeviceRole: store.DeviceRoleManager, AllLibraries: true}
	if err := service.VerifyAdminReauth(ctx, tokenScope, "right-password"); !errors.Is(err, ErrAdminPassword) {
		t.Fatalf("token VerifyAdminReauth err = %v, want ErrAdminPassword", err)
	}

	// Exhausting failed attempts locks out the lane even for the right password.
	for i := 2; i < adminBackoff.FreeAttempts; i++ {
		if err := service.VerifyAdminReauth(ctx, adminScope, "wrong-password"); !errors.Is(err, ErrAdminPassword) {
			t.Fatalf("attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	if err := service.VerifyAdminReauth(ctx, adminScope, "right-password"); !errors.Is(err, ErrAdminLocked) {
		t.Fatalf("post-budget VerifyAdminReauth err = %v, want ErrAdminLocked", err)
	}
}
