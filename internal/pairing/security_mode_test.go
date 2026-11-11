package pairing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestBootstrapStoresSecurityMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want store.SignInSecurityMode
	}{{"", store.SignInSecurityHome}, {"home", store.SignInSecurityHome}, {"strict", store.SignInSecurityStrict}} {
		t.Run("mode="+tc.in, func(t *testing.T) {
			ctx := context.Background()
			st := storetest.Open(t)
			service := deterministicService(st, time.Unix(0, 0).UTC())
			service.SetBootstrapToken("setup-token")
			if _, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser", SecurityMode: tc.in}); err != nil {
				t.Fatalf("Bootstrap: %v", err)
			}
			if mode, err := service.SignInSecurityMode(ctx); err != nil || mode != tc.want {
				t.Fatalf("SignInSecurityMode = (%q, %v), want %q", mode, err, tc.want)
			}
		})
	}
}

func TestBootstrapRejectsUnknownSecurityMode(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	service.SetBootstrapToken("setup-token")
	if _, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser", SecurityMode: "lax"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Bootstrap(lax) err = %v, want ErrInvalidRequest", err)
	}
	// The refusal spent neither the token nor a setup attempt.
	if _, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser"}); err != nil {
		t.Fatalf("Bootstrap after refused mode: %v", err)
	}
}

// adminSignInStore returns a store with an owner and admin password "correct
// horse" in the given mode.
func adminSignInStore(t *testing.T, mode store.SignInSecurityMode, now time.Time) *store.Store {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t)
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSignInSecurityMode(ctx, mode, now); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAdminSignInStrictRefusesCorrectPasswordLikeWrongOne(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000_000, 0).UTC()
	strict := adminSignInStore(t, store.SignInSecurityStrict, now)
	home := adminSignInStore(t, store.SignInSecurityHome, now)
	strictService := deterministicService(strict, now)
	homeService := deterministicService(home, now)
	addr := "192.168.1.5:5000"

	// Strict with the right password and Home with a wrong one must look the
	// same: the same error, the same backoff state, the same audit trail.
	free := auth.AdminBackoff().FreeAttempts
	for i := 0; i < free; i++ {
		_, strictErr := strictService.AdminSignIn(ctx, AdminSignInRequest{Password: "correct horse", DeviceName: "Laptop", RemoteAddr: addr})
		_, homeErr := homeService.AdminSignIn(ctx, AdminSignInRequest{Password: "wrong horse", DeviceName: "Laptop", RemoteAddr: addr})
		for name, err := range map[string]error{"strict": strictErr, "home": homeErr} {
			if !errors.Is(err, ErrSignInFailed) || err.Error() != "sign-in failed" || refusalReason(err) != auth.RefusalAdminPasswordRejected {
				t.Fatalf("attempt %d %s: err = %v, want uniform sign-in failure", i, name, err)
			}
		}
	}
	curve := auth.AdminBackoff()
	for _, key := range []string{setupAttemptKey(addr), adminSignInGlobalKey} {
		strictUntil, err := strict.AuthBackoffUntil(ctx, adminSignInAttemptAction, key, now, curve)
		if err != nil {
			t.Fatal(err)
		}
		homeUntil, err := home.AuthBackoffUntil(ctx, adminSignInAttemptAction, key, now, curve)
		if err != nil {
			t.Fatal(err)
		}
		if !strictUntil.Equal(homeUntil) || !now.Before(strictUntil) {
			t.Fatalf("%s backoff: strict %v, home %v, want equal and locked", key, strictUntil, homeUntil)
		}
	}
	for name, st := range map[string]*store.Store{"strict": strict, "home": home} {
		entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		denied := 0
		for _, entry := range entries {
			if entry.Action == store.ActivityAdminLoginDenied {
				denied++
			}
			if entry.Action == store.ActivityEnroll {
				t.Fatalf("%s: an enrollment was recorded", name)
			}
		}
		if denied != free {
			t.Fatalf("%s: %d denied audit entries, want %d", name, denied, free)
		}
	}
}

func TestAdminSignInHomeAllowsCorrectPassword(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000_000, 0).UTC()
	st := adminSignInStore(t, store.SignInSecurityHome, now)
	if _, err := deterministicService(st, now).AdminSignIn(ctx, AdminSignInRequest{Password: "correct horse", DeviceName: "Laptop", RemoteAddr: "192.168.1.5:5000"}); err != nil {
		t.Fatalf("AdminSignIn in home mode: %v", err)
	}
}

func TestStrictModeKeepsRenewalAndOwnerCodes(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000_000, 0).UTC()
	st := adminSignInStore(t, store.SignInSecurityStrict, now)
	enrollBrowser(t, st, "device-browser", "session-lapsed", now.Add(-time.Hour))
	service := deterministicService(st, now)
	service.generateID = func(string) (string, error) { return "session-renewed", nil }
	if _, err := service.RenewSession(ctx, RenewRequest{
		DeviceID: "device-browser", DeviceSecret: "browser-secret", OldSessionID: "session-lapsed", RemoteAddr: "192.168.1.5:5000",
	}); err != nil {
		t.Fatalf("RenewSession in strict mode: %v", err)
	}
	if _, err := service.CreateOwnerCode(ctx, ""); err != nil {
		t.Fatalf("CreateOwnerCode in strict mode: %v", err)
	}
}
