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

// CORE-1132: an owner who finished setup without a password chooses the first
// admin password from the Console gate instead of facing an unlock with nothing
// to check against.

func firstPasswordEntries(t *testing.T, st *store.Store, action string) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ActivityEntry
	for _, entry := range entries {
		if entry.Action == action {
			out = append(out, entry)
		}
	}
	return out
}

func TestOpenAdminSessionCreatesFirstPasswordAndOpensWindow(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")
	if set, err := st.AdminPasswordSet(ctx); err != nil || set {
		t.Fatalf("fixture already has an admin password: (%v, %v)", set, err)
	}

	until, err := service.OpenAdminSession(ctx, scope, "first-password", true)
	if err != nil {
		t.Fatalf("OpenAdminSession(createFirst) = %v", err)
	}
	if want := now.Add(store.DefaultAdminSessionMinutes * time.Minute).UTC(); !until.Equal(want) {
		t.Fatalf("window ends %v, want %v", until, want)
	}
	if err := st.VerifyAdminPassword(ctx, "first-password"); err != nil {
		t.Fatalf("stored password does not verify: %v", err)
	}
	reopened := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")
	if !reopened.Admin {
		t.Fatal("the Admin Session window is not open after the first password was set")
	}
	set := firstPasswordEntries(t, st, store.ActivityAdminPasswordSet)
	if len(set) != 1 || set[0].SessionID != "session-manager" || set[0].ProfileID != scope.ProfileID {
		t.Fatalf("audit entries = %+v, want one admin.password.set for the caller", set)
	}
	if strings.Contains(set[0].TargetID+set[0].TargetType, "first-password") {
		t.Fatal("audit entry carries the password")
	}
}

func TestOpenAdminSessionNeverReplacesAnExistingPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetAdminPassword(ctx, "right-password", now); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	// createFirst is an ordinary unlock once a password exists: a wrong password
	// is the same generic refusal and the stored password is untouched.
	if _, err := service.OpenAdminSession(ctx, scope, "attacker-password", true); !errors.Is(err, ErrAdminPassword) {
		t.Fatalf("createFirst against an existing password = %v, want ErrAdminPassword", err)
	}
	if err := st.VerifyAdminPassword(ctx, "right-password"); err != nil {
		t.Fatalf("existing password changed: %v", err)
	}
	if err := st.VerifyAdminPassword(ctx, "attacker-password"); err == nil {
		t.Fatal("createFirst replaced the existing password")
	}
	if got := firstPasswordEntries(t, st, store.ActivityAdminPasswordSet); len(got) != 0 {
		t.Fatalf("audited a password set that did not happen: %+v", got)
	}
	// The right password still unlocks through the same call.
	if _, err := service.OpenAdminSession(ctx, scope, "right-password", true); err != nil {
		t.Fatalf("ordinary unlock via createFirst = %v", err)
	}
}

func TestOpenAdminSessionWithoutCreateFirstDoesNotSetAPassword(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	if _, err := service.OpenAdminSession(ctx, scope, "typed-at-unlock", false); !errors.Is(err, ErrAdminPassword) {
		t.Fatalf("unlock with no password set = %v, want ErrAdminPassword", err)
	}
	if set, err := st.AdminPasswordSet(ctx); err != nil || set {
		t.Fatalf("an unlock set the password: (%v, %v)", set, err)
	}
}

func TestOpenAdminSessionCreateFirstRefusesNonManagerAndBlank(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := NewServiceWithClock(st, func() time.Time { return now })
	viewer := signInSecurityScope(t, service, "session-default", "device-tv", "dev-tv-secret")

	if _, err := service.OpenAdminSession(ctx, viewer, "first-password", true); !errors.Is(err, ErrAdminPassword) {
		t.Fatalf("non-manager createFirst = %v, want ErrAdminPassword", err)
	}
	manager := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")
	if _, err := service.OpenAdminSession(ctx, manager, "   ", true); err == nil {
		t.Fatal("blank first password accepted")
	}
	if set, err := st.AdminPasswordSet(ctx); err != nil || set {
		t.Fatalf("a refused request set the password: (%v, %v)", set, err)
	}
	if denied := firstPasswordEntries(t, st, store.ActivityAdminLoginDenied); len(denied) != 1 {
		t.Fatalf("denied audit entries = %d, want 1", len(denied))
	}
}

func TestOpenAdminSessionCreateFirstIsThrottled(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := NewServiceWithClock(st, func() time.Time { return now })
	viewer := signInSecurityScope(t, service, "session-default", "device-tv", "dev-tv-secret")

	var last error
	for i := 0; i < adminBackoff.FreeAttempts+2; i++ {
		_, last = service.OpenAdminSession(ctx, viewer, "first-password", true)
	}
	if !errors.Is(last, ErrAdminLocked) {
		t.Fatalf("after the free budget err = %v, want ErrAdminLocked", last)
	}
}

// CORE-2303: the Admin Session length is the owner's setting, 0 means it never
// expires, and locking the Console still ends a never-expiring one.
func TestAdminSessionLengthFollowsTheOwnerSetting(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := NewServiceWithClock(st, func() time.Time { return now })
	scope := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")

	minutes := 120
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{AdminSessionMinutes: &minutes}, now); err != nil {
		t.Fatal(err)
	}
	until, err := service.OpenAdminSession(ctx, scope, "first-password", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(2 * time.Hour); !until.Equal(want) {
		t.Fatalf("window ends %v, want %v", until, want)
	}

	never := 0
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{AdminSessionMinutes: &never}, now); err != nil {
		t.Fatal(err)
	}
	until, err = service.BeginAdminSession(ctx, scope, "first-password")
	if err != nil {
		t.Fatal(err)
	}
	if !until.Equal(store.SessionNeverExpires) {
		t.Fatalf("window ends %v, want never", until)
	}
	now = now.Add(365 * 24 * time.Hour)
	open := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret")
	if !open.Admin {
		t.Fatal("a never-expiring Admin Session closed on its own")
	}
	if err := service.EndAdminSession(ctx, open); err != nil {
		t.Fatal(err)
	}
	if locked := signInSecurityScope(t, service, "session-manager", "device-mac", "dev-manager-secret"); locked.Admin {
		t.Fatal("locking the Console left a never-expiring Admin Session open")
	}
}
