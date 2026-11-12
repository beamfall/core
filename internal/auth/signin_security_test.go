package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func signInSecurityScope(t *testing.T, service *Service, session, device, secret string) Scope {
	t.Helper()
	scope, err := service.ScopeFromRequest(context.Background(), authenticatedRequest(session, device, secret))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func signInSecurityAudit(t *testing.T, st *store.Store) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ActivityEntry
	for _, entry := range entries {
		if entry.Action == store.ActivitySignInSecurityMode {
			out = append(out, entry)
		}
	}
	return out
}

func TestChangeSignInSecurityModeWithPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	old, err := service.ChangeSignInSecurityMode(ctx, scope, store.SignInSecurityStrict, "right-password")
	if err != nil || old != store.SignInSecurityHome {
		t.Fatalf("ChangeSignInSecurityMode(strict) = (%q, %v), want old home", old, err)
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != store.SignInSecurityStrict {
		t.Fatalf("stored mode = (%q, %v), want strict", mode, err)
	}
	audit := signInSecurityAudit(t, st)
	if len(audit) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(audit))
	}
	entry := audit[0]
	if entry.TargetType != "signin_security_mode" || entry.TargetID != "home->strict" ||
		entry.SessionID != "session-manager" || entry.DeviceID != "device-mac" || entry.ProfileID != scope.ProfileID {
		t.Fatalf("audit entry = %+v", entry)
	}
	if strings.Contains(entry.TargetID+entry.TargetType, "right-password") {
		t.Fatal("audit entry carries the password")
	}
}

func TestChangeSignInSecurityModeRefusesWrongOrMissingPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	for _, password := range []string{"wrong", ""} {
		if _, err := service.ChangeSignInSecurityMode(ctx, scope, store.SignInSecurityStrict, password); !errors.Is(err, ErrAdminPassword) {
			t.Fatalf("password %q: err = %v, want ErrAdminPassword", password, err)
		}
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != store.SignInSecurityHome {
		t.Fatalf("stored mode = (%q, %v), want home", mode, err)
	}
	if audit := signInSecurityAudit(t, st); len(audit) != 0 {
		t.Fatalf("refused switches audited as changes: %+v", audit)
	}
	// Refusals share the admin re-auth lane: the budget runs out on the same
	// curve, after which even the right password is throttled.
	for i := 2; i < adminBackoff.FreeAttempts; i++ {
		if _, err := service.BeginAdminSession(ctx, scope, "wrong"); !errors.Is(err, ErrAdminPassword) {
			t.Fatalf("attempt %d err = %v, want ErrAdminPassword", i, err)
		}
	}
	if _, err := service.ChangeSignInSecurityMode(ctx, scope, store.SignInSecurityStrict, "right-password"); !errors.Is(err, ErrAdminLocked) {
		t.Fatalf("post-budget err = %v, want ErrAdminLocked", err)
	}
}

func TestChangeSignInSecurityModeRequiresManagerAndKnownMode(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	viewer := signInSecurityScope(t, service, "session-default", "device-tv", "dev-tv-secret")
	if _, err := service.ChangeSignInSecurityMode(ctx, viewer, store.SignInSecurityStrict, "right-password"); !errors.Is(err, ErrNotManager) {
		t.Fatalf("viewer err = %v, want ErrNotManager", err)
	}
	manager := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")
	var verr store.ValidationError
	for _, mode := range []store.SignInSecurityMode{"", "open"} {
		if _, err := service.ChangeSignInSecurityMode(ctx, manager, mode, "right-password"); !errors.As(err, &verr) {
			t.Fatalf("mode %q err = %v, want ValidationError", mode, err)
		}
	}
	if mode, err := st.SignInSecurityMode(ctx); err != nil || mode != store.SignInSecurityHome {
		t.Fatalf("stored mode = (%q, %v), want home", mode, err)
	}
}
