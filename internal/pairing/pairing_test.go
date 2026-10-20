package pairing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestCreateCodeRequiresManagerDevice(t *testing.T) {
	st := storetest.Open(t)
	authService := auth.NewServiceWithClock(st, func() time.Time { return time.Unix(0, 0).UTC() })
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(st, func() time.Time { return time.Unix(0, 0).UTC() })

	_, err = service.CreateCode(context.Background(), scope, CodeRequest{})

	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateCode error = %v, want ErrForbidden", err)
	}
}

func TestCreateCodeRequiresAdminSession(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	scope, err := authService.Scope(context.Background(), "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	if scope.DeviceRole != store.DeviceRoleManager || scope.Admin {
		t.Fatalf("scope role/admin = (%q, %v), want manager/non-admin", scope.DeviceRole, scope.Admin)
	}
	service := deterministicService(st, now)

	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateCode without admin = %v, want ErrForbidden", err)
	}
	if _, err := service.CreateCode(context.Background(), managerAdminScope(t, st, now), CodeRequest{}); err != nil {
		t.Fatalf("CreateCode with admin = %v, want nil", err)
	}
}

func TestCreateCodeForbidsOutOfScopeMinting(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	service := deterministicService(st, now)

	managerScope, err := authService.Scope(context.Background(), "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	if managerScope.Admin {
		t.Fatal("seed manager scope unexpectedly admin")
	}

	// A managed Profile bound to a manager-role device: even with an admin
	// window stamped on the session, managed Profiles have no admin path
	//, so the scope must never mint pairing codes.
	if err := st.EnrollDevice(context.Background(), store.Device{
		ID:         "device-managed-manager",
		Name:       "Managed Manager",
		Role:       store.DeviceRoleManager,
		SecretHash: store.HashDeviceSecret("managed-manager-secret"),
	}, store.Session{
		ID:        "session-managed-manager",
		DeviceID:  "device-managed-manager",
		ProfileID: "profile-managed",
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionAdminUntil(context.Background(), "session-managed-manager", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	managedScope, err := authService.Scope(context.Background(), "session-managed-manager")
	if err != nil {
		t.Fatal(err)
	}
	if managedScope.DeviceRole != store.DeviceRoleManager || managedScope.Profile != store.ProfileManaged || managedScope.Admin {
		t.Fatalf("managed scope = (%q, %q, admin=%v), want manager device, managed profile, non-admin",
			managedScope.DeviceRole, managedScope.Profile, managedScope.Admin)
	}

	cases := []struct {
		name  string
		scope auth.Scope
		req   CodeRequest
	}{
		{"managed profile on manager device", managedScope, CodeRequest{}},
		{"non-admin manager-role code", managerScope, CodeRequest{DeviceRole: string(store.DeviceRoleManager)}},
		{"non-admin another profile's code", managerScope, CodeRequest{ProfileID: "profile-managed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := service.CreateCode(context.Background(), tc.scope, tc.req); !errors.Is(err, ErrForbidden) {
				t.Fatalf("CreateCode error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestCreateCodeAndEnrollDevice(t *testing.T) {
	// feature:pairing-enroll
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	scope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)

	code, err := service.CreateCode(context.Background(), scope, CodeRequest{
		DeviceRole: string(store.DeviceRoleViewer),
	})
	if err != nil {
		t.Fatalf("CreateCode returned error: %v", err)
	}
	if code.Code != "PAIR1234" {
		t.Fatalf("Code = %q, want PAIR1234", code.Code)
	}

	enrolled, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "pair-1234",
		DeviceName: "Living Room",
		IsLAN:      true,
	})
	if err != nil {
		t.Fatalf("Enroll returned error: %v", err)
	}
	if enrolled.DeviceID != "device-new" {
		t.Fatalf("DeviceID = %q, want device-new", enrolled.DeviceID)
	}
	if enrolled.SessionID != "session-new" {
		t.Fatalf("SessionID = %q, want session-new", enrolled.SessionID)
	}
	if enrolled.DeviceSecret != "device-secret-new" {
		t.Fatalf("DeviceSecret = %q, want device-secret-new", enrolled.DeviceSecret)
	}
	if enrolled.SessionExpiresAt != "1970-01-31T00:00:00Z" {
		t.Fatalf("SessionExpiresAt = %q, want 1970-01-31T00:00:00Z", enrolled.SessionExpiresAt)
	}

	enrolledScope, err := authService.Scope(context.Background(), store.SessionID(enrolled.SessionID))
	if err != nil {
		t.Fatalf("Scope returned error for enrolled session: %v", err)
	}
	if enrolledScope.Revealed {
		t.Fatal("enrolled session starts revealed")
	}
	if enrolledScope.DeviceRole != store.DeviceRoleViewer {
		t.Fatalf("DeviceRole = %q, want viewer", enrolledScope.DeviceRole)
	}
	credentialScope, err := authService.ScopeWithDeviceCredential(
		context.Background(),
		store.SessionID(enrolled.SessionID),
		store.DeviceID(enrolled.DeviceID),
		enrolled.DeviceSecret,
	)
	if err != nil {
		t.Fatalf("ScopeWithDeviceCredential returned error: %v", err)
	}
	if credentialScope.SessionID != store.SessionID(enrolled.SessionID) {
		t.Fatalf("SessionID = %q, want %q", credentialScope.SessionID, enrolled.SessionID)
	}
	device, err := st.Device(context.Background(), store.DeviceID(enrolled.DeviceID))
	if err != nil {
		t.Fatal(err)
	}
	if device.DistributionClass != store.DeviceDistributionDirect {
		t.Fatalf("DistributionClass = %q, want direct", device.DistributionClass)
	}
	if device.SecretHash == enrolled.DeviceSecret {
		t.Fatal("raw device secret was stored")
	}
}

func TestEnrollPersistsDistributionClass(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	scope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{DeviceRole: string(store.DeviceRoleViewer)}); err != nil {
		t.Fatal(err)
	}
	enrolled, err := service.Enroll(context.Background(), EnrollRequest{
		Code:              "PAIR1234",
		DeviceName:        "iPad",
		DistributionClass: "appStore",
		IsLAN:             true,
	})
	if err != nil {
		t.Fatalf("Enroll returned error: %v", err)
	}
	device, err := st.Device(context.Background(), store.DeviceID(enrolled.DeviceID))
	if err != nil {
		t.Fatalf("Device returned error: %v", err)
	}
	if device.DistributionClass != store.DeviceDistributionAppStore {
		t.Fatalf("DistributionClass = %q, want appStore", device.DistributionClass)
	}
}

func TestPairingCodeIsOneTimeUse(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	scope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true}); err != nil {
		t.Fatalf("Enroll returned error: %v", err)
	}

	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})

	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestDemoEnrollmentCreatesReusableViewerSession(t *testing.T) {
	// feature:store-review-demo-enrollment
	st := storetest.Open(t)
	authService := auth.NewServiceWithClock(st, func() time.Time { return time.Unix(0, 0).UTC() })
	service := deterministicService(st, time.Unix(0, 0).UTC())
	ids := []string{"device-demo-1", "session-demo-1", "device-demo-2", "session-demo-2"}
	service.generateID = func(_ string) (string, error) {
		next := ids[0]
		ids = ids[1:]
		return next, nil
	}
	if err := service.SetDemoEnrollment(DemoEnrollment{
		Key:       "review-demo-key-123456",
		ProfileID: "profile-managed",
	}); err != nil {
		t.Fatalf("SetDemoEnrollment: %v", err)
	}

	enrolled, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "review demo key 123456",
		DeviceName: "Review Device",
		IsLAN:      true,
	})
	if err != nil {
		t.Fatalf("Enroll returned error: %v", err)
	}
	if enrolled.DeviceRole != string(store.DeviceRoleViewer) {
		t.Fatalf("DeviceRole = %q, want viewer", enrolled.DeviceRole)
	}
	if enrolled.ProfileID != "profile-managed" {
		t.Fatalf("ProfileID = %q, want profile-managed", enrolled.ProfileID)
	}

	scope, err := authService.ScopeWithDeviceCredential(context.Background(), store.SessionID(enrolled.SessionID), store.DeviceID(enrolled.DeviceID), enrolled.DeviceSecret)
	if err != nil {
		t.Fatalf("ScopeWithDeviceCredential: %v", err)
	}
	if scope.DeviceRole != store.DeviceRoleViewer || scope.Admin {
		t.Fatalf("scope role/admin = (%q, %v), want viewer/non-admin", scope.DeviceRole, scope.Admin)
	}
	if scope.ProfileID != "profile-managed" {
		t.Fatalf("scope ProfileID = %q, want profile-managed", scope.ProfileID)
	}

	second, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "review-demo-key-123456",
		DeviceName: "Second Review Device",
		IsLAN:      true,
	})
	if err != nil {
		t.Fatalf("second Enroll returned error: %v", err)
	}
	if second.DeviceID == enrolled.DeviceID || second.SessionID == enrolled.SessionID {
		t.Fatalf("second enrollment reused identity: first=%+v second=%+v", enrolled, second)
	}
}

func TestDemoEnrollmentRejectsWeakOrMissingGrant(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())

	if err := service.SetDemoEnrollment(DemoEnrollment{Key: "short", ProfileID: "profile-default"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("weak SetDemoEnrollment error = %v, want ErrInvalidRequest", err)
	}
	if err := service.SetDemoEnrollment(DemoEnrollment{Key: "review-demo-key-123456"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing-profile SetDemoEnrollment error = %v, want ErrInvalidRequest", err)
	}
	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "review-demo-key-123456", DeviceName: "Review Device", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll with disabled demo grant error = %v, want ErrInvalidCode", err)
	}
}

func TestDemoEnrollmentAttemptsUseHashedThrottleKey(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	service := deterministicService(st, now)
	const key = "review-demo-key-123456"
	if err := service.SetDemoEnrollment(DemoEnrollment{
		Key:       key,
		ProfileID: "missing-profile",
	}); err != nil {
		t.Fatalf("SetDemoEnrollment: %v", err)
	}

	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: key, DeviceName: "Review Device", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll with missing demo profile error = %v, want ErrInvalidCode", err)
	}

	policy := store.AuthBackoff{Window: time.Minute, FreeAttempts: 1, BaseDelay: time.Minute, MaxDelay: time.Minute}
	rawUntil, err := st.AuthBackoffUntil(context.Background(), enrollAttemptAction, codeAttemptKey(normalizeCode(key)), now, policy)
	if err != nil {
		t.Fatalf("AuthBackoffUntil(raw): %v", err)
	}
	if !rawUntil.IsZero() {
		t.Fatalf("raw demo key throttle armed until %v; raw credential was persisted", rawUntil)
	}
	hashedUntil, err := st.AuthBackoffUntil(context.Background(), enrollAttemptAction, demoAttemptKey(normalizeCode(key)), now, policy)
	if err != nil {
		t.Fatalf("AuthBackoffUntil(hashed): %v", err)
	}
	if hashedUntil.IsZero() {
		t.Fatal("hashed demo throttle key was not recorded")
	}
}

func TestPairingCodeAttemptsUseHashedThrottleKey(t *testing.T) {
	policy := store.AuthBackoff{Window: time.Minute, FreeAttempts: 1, BaseDelay: time.Minute, MaxDelay: time.Minute}
	assertNoRawCode := func(t *testing.T, st *store.Store, code string, now time.Time) {
		t.Helper()
		rawUntil, err := st.AuthBackoffUntil(context.Background(), enrollAttemptAction, "code:"+normalizeCode(code), now, policy)
		if err != nil {
			t.Fatalf("AuthBackoffUntil(raw): %v", err)
		}
		if !rawUntil.IsZero() {
			t.Fatalf("raw code throttle armed until %v; raw credential was persisted", rawUntil)
		}
		hashedUntil, err := st.AuthBackoffUntil(context.Background(), enrollAttemptAction, codeAttemptKey(normalizeCode(code)), now, policy)
		if err != nil {
			t.Fatalf("AuthBackoffUntil(hashed): %v", err)
		}
		if hashedUntil.IsZero() {
			t.Fatal("hashed pairing-code throttle key was not recorded")
		}
	}

	t.Run("role mismatch promote", func(t *testing.T) {
		st := storetest.Open(t)
		now := time.Unix(100, 0).UTC()
		service := deterministicService(st, now)
		if _, err := service.CreateCode(context.Background(), managerAdminScope(t, st, now), CodeRequest{DeviceRole: string(store.DeviceRoleViewer)}); err != nil {
			t.Fatal(err)
		}
		authService := auth.NewServiceWithClock(st, func() time.Time { return now })
		viewerScope, err := authService.Scope(context.Background(), "session-default")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "PAIR1234"}); !errors.Is(err, ErrRoleNotGranted) {
			t.Fatalf("Promote error = %v, want ErrRoleNotGranted", err)
		}
		assertNoRawCode(t, st, "PAIR1234", now)
	})

	t.Run("disabled profile enroll", func(t *testing.T) {
		st := storetest.Open(t)
		now := time.Unix(100, 0).UTC()
		service := deterministicService(st, now)
		service.generateCode = func() (string, error) { return "DISABLED1", nil }
		if _, err := service.CreateCode(context.Background(), managerAdminScope(t, st, now), CodeRequest{DeviceRole: string(store.DeviceRoleViewer)}); err != nil {
			t.Fatal(err)
		}
		disabled := true
		if _, err := st.UpdateProfileSettings(context.Background(), "profile-default", store.ProfileSettings{Disabled: &disabled}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "DISABLED1", DeviceName: "TV", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll error = %v, want ErrInvalidCode", err)
		}
		assertNoRawCode(t, st, "DISABLED1", now)
	})
}

func TestEnrollDisabledProfileAnswersInvalidCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), managerAdminScope(t, st, now), CodeRequest{}); err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := st.UpdateProfileSettings(context.Background(), "profile-default", store.ProfileSettings{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	// A valid code whose Profile is disabled must answer exactly like a
	// garbage code — never a 500 that leaks the code (and Profile) exist.
	_, validErr := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})
	if !errors.Is(validErr, ErrInvalidCode) {
		t.Fatalf("Enroll valid code on disabled Profile = %v, want ErrInvalidCode", validErr)
	}
	_, garbageErr := service.Enroll(context.Background(), EnrollRequest{Code: "WRONG999", DeviceName: "TV", IsLAN: true})
	if garbageErr != validErr {
		t.Fatalf("disabled-Profile answer %v differs from garbage-code answer %v; code existence is enumerable", validErr, garbageErr)
	}
}

func TestExpiredPairingCodeIsRejected(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	scope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{}); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(11 * time.Minute) }

	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})

	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestPairingCodeAtExpiryBoundaryIsRejected(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	scope := managerAdminScope(t, st, now)
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{}); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(10 * time.Minute) }

	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})

	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestEnrollThrottleUsesNormalizedCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	// Differently-cased/spaced renderings of one code normalize to the SAME
	// throttle key, so they spend a single per-code budget.
	for _, code := range []string{"pair-1234", "PAIR1234", "pair 1234", "Pair-1234", "PAIR 1234"} {
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: code, DeviceName: "TV", IsLAN: true}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}

	// The free-attempt budget for that normalized key is now spent: the next
	// attempt is locked out by the exponential backoff.
	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})

	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("Enroll error = %v, want ErrThrottled", err)
	}
}

func TestEnrollThrottleUsesRemoteAddressAcrossDifferentCodes(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	enableRemoteEnrollment(t, st, now)
	remoteAddr := "203.0.113.50:5000"
	// Different codes from one remote IP all spend the same per-remote budget
	//: the lane is keyed by remote address, not by code.
	for _, code := range []string{"BAD0001", "BAD0002", "BAD0003", "BAD0004", "BAD0005"} {
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: code, DeviceName: "TV", RemoteAddr: remoteAddr}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}

	// The per-remote budget is spent: a further code from the same IP (different
	// port) is locked out by the backoff.
	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "BAD0006", DeviceName: "TV", RemoteAddr: "203.0.113.50:6000"})

	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("Enroll error = %v, want ErrThrottled", err)
	}
}

func TestEnrollThrottleGroupsIPv6Prefix(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	enableRemoteEnrollment(t, st, now)
	for i, code := range []string{"BAD0001", "BAD0002", "BAD0003", "BAD0004", "BAD0005"} {
		addr := "2001:db8::" + string(rune('1'+i)) + ":5000"
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: code, DeviceName: "TV", RemoteAddr: addr}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}

	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "BAD0006", DeviceName: "TV", RemoteAddr: "[2001:db8::6]:6000"})
	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("Enroll error = %v, want ErrThrottled", err)
	}
}

func TestEnrollThrottleTreatsIPv4MappedIPv6AsIPv4(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	enableRemoteEnrollment(t, st, now)
	for i := 0; i < enrollBackoff.FreeAttempts; i++ {
		code := fmt.Sprintf("BAD%04d", i)
		_, err := service.Enroll(context.Background(), EnrollRequest{
			Code:       code,
			DeviceName: "TV",
			RemoteAddr: "203.0.113.50:6000",
		})
		if !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll attempt %d error = %v, want ErrInvalidCode", i, err)
		}
	}

	_, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "BAD9999",
		DeviceName: "TV",
		RemoteAddr: "[::ffff:203.0.113.50]:6000",
	})
	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("IPv4-mapped retry error = %v, want ErrThrottled", err)
	}
}

// Evidence — SECAUDIT-20260817-ENROLL-LOCKOUT-DOS-1 (2026-08-20, red-first)
//
// Red (vulnerable code, this test added but fix not yet applied):
//
//	command: go test ./internal/pairing ./internal/server -run 'TestEnrollDistributedFailures' -count=1
//	worktree: f0a4db6e3d7b07c33561cd3d65b4ef33ddc7acd9 + these tests uncommitted
//	exit 1, elapsed 5s, UTC 2026-08-20T11:32:02Z..11:32:07Z
//	log: .agent-evidence/SECAUDIT-20260817-ENROLL-LOCKOUT-DOS-1-red-first.log (untracked, human copy)
//	observed: pairing_test.go: Enroll("BAD0025", "[2001:db8:19::1]:5000") error = pairing attempts throttled, want ErrInvalidCode
//	— the 26th distributed failure spent the former server-wide lane and denied
//	unrelated enrollment (the DoS this ticket removes).
//
// Green (fix applied: server-wide "global:enroll" lane removed):
//
//	command: go vet ./internal/pairing ./internal/server && go test ./internal/pairing ./internal/server -count=1
//	worktree: f0a4db6e3d7b07c33561cd3d65b4ef33ddc7acd9 + this change
//	vet exit 0; test exit 0, elapsed 48s, UTC 2026-08-20T11:33:51Z..11:34:39Z
//	log: .agent-evidence/SECAUDIT-20260817-ENROLL-LOCKOUT-DOS-1-green.log (untracked, human copy)
//	ok internal/pairing 6.745s; ok internal/server 46.504s
//
// TestEnrollThrottleUsesGlobalFailedRedemptionLane was removed in the same
// change: it asserted the server-wide denial lane this ticket mandates
// removing; this test supersedes it with the corrected invariant. All other
// throttle assertions (per-code normalized, per-remote, IPv6 /64 grouping,
// IPv4-mapped, promotion budget sharing, non-oracle remote gate) are unchanged
// and still passing in the green run above.
func TestEnrollDistributedFailuresDoNotDenyUnrelatedValidCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)
	scope := managerAdminScope(t, st, now)
	enableRemoteEnrollment(t, st, now)
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{DeviceRole: string(store.DeviceRoleViewer)}); err != nil {
		t.Fatal(err)
	}

	// Distributed unauthenticated failures: distinct wrong codes, each from a
	// distinct IPv6 /64 source prefix. Each attempt spends only its own
	// per-code and per-source budget; no server-wide lane may accumulate, so
	// every attempt answers ErrInvalidCode.
	for i := 0; i < 40; i++ {
		code := fmt.Sprintf("BAD%04d", i)
		addr := fmt.Sprintf("[2001:db8:%x::1]:5000", i)
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: code, DeviceName: "TV", RemoteAddr: addr}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Enroll(%q, %q) error = %v, want ErrInvalidCode", code, addr, err)
		}
	}

	// An unrelated valid fresh code from an uninvolved source still redeems:
	// unauthenticated failures elsewhere never deny a valid enrollment.
	enrolled, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", RemoteAddr: "[2001:db9::1]:5000"})
	if err != nil {
		t.Fatalf("valid enroll after distributed failures = %v, want success", err)
	}
	if enrolled.DeviceID != "device-new" {
		t.Fatalf("DeviceID = %q, want device-new", enrolled.DeviceID)
	}

	// Same-code brute force stays throttled: the per-code lane still spends
	// even when every attempt arrives from a fresh source prefix.
	for i := 0; i < enrollBackoff.FreeAttempts; i++ {
		addr := fmt.Sprintf("[2001:dba:%x::1]:5000", i)
		if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "REPEAT01", DeviceName: "TV", RemoteAddr: addr}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("same-code attempt %d error = %v, want ErrInvalidCode", i, err)
		}
	}
	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "REPEAT01", DeviceName: "TV", RemoteAddr: "[2001:dbb::1]:5000"}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("same-code lockout error = %v, want ErrThrottled", err)
	}
}

func TestRemoteEnrollmentDisabledDoesNotOracleInvalidCode(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := deterministicService(st, now)

	_, err := service.Enroll(context.Background(), EnrollRequest{Code: "BAD0001", DeviceName: "TV", RemoteAddr: "203.0.113.50:5000"})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("Enroll invalid remote-disabled code error = %v, want ErrInvalidCode", err)
	}
}

func TestTwoPairingServicesRedeemCodeOnlyOnce(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	scope := managerAdminScope(t, st, now)
	first := deterministicService(st, now)
	second := deterministicService(st, now)
	if _, err := first.CreateCode(context.Background(), scope, CodeRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true}); err != nil {
		t.Fatalf("second service enroll returned error: %v", err)
	}

	_, err := first.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true})

	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("first service enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestPromoteUpgradesDeviceToManager(t *testing.T) {
	// feature:device-promotion
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	managerScope := managerAdminScope(t, st, now)
	viewerScope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if viewerScope.DeviceRole != store.DeviceRoleViewer {
		t.Fatalf("seed DeviceRole = %q, want viewer", viewerScope.DeviceRole)
	}
	service := deterministicService(st, now)
	service.generateID = func(prefix string) (string, error) { return prefix + "-rotated", nil }
	if _, err := service.CreateCode(context.Background(), managerScope, CodeRequest{
		DeviceRole: string(store.DeviceRoleManager),
	}); err != nil {
		t.Fatal(err)
	}

	promoted, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "pair-1234"})

	if err != nil {
		t.Fatalf("Promote returned error: %v", err)
	}
	if promoted.DeviceID != string(viewerScope.DeviceID) {
		t.Fatalf("DeviceID = %q, want %q", promoted.DeviceID, viewerScope.DeviceID)
	}
	if promoted.DeviceRole != string(store.DeviceRoleManager) {
		t.Fatalf("DeviceRole = %q, want manager", promoted.DeviceRole)
	}
	if promoted.SessionID == "session-default" || promoted.SessionID == "" {
		t.Fatalf("SessionID = %q, want rotated", promoted.SessionID)
	}
	if promoted.DeviceSecret == "dev-tv-secret" || promoted.DeviceSecret == "" {
		t.Fatalf("DeviceSecret = %q, want rotated", promoted.DeviceSecret)
	}
	if _, err := authService.Scope(context.Background(), "session-default"); err == nil {
		t.Fatal("old session still resolves after promotion")
	}
	if _, err := authService.ScopeWithDeviceCredential(context.Background(), store.SessionID(promoted.SessionID), viewerScope.DeviceID, "dev-tv-secret"); err == nil {
		t.Fatal("old device secret still resolves after promotion")
	}
	upgraded, err := authService.ScopeWithDeviceCredential(context.Background(), store.SessionID(promoted.SessionID), viewerScope.DeviceID, promoted.DeviceSecret)
	if err != nil {
		t.Fatalf("rotated credential did not resolve: %v", err)
	}
	if upgraded.DeviceRole != store.DeviceRoleManager {
		t.Fatalf("post-promotion DeviceRole = %q, want manager", upgraded.DeviceRole)
	}
	// The code was single-use: a second promotion attempt is rejected.
	if _, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "pair-1234"}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second Promote error = %v, want ErrInvalidCode", err)
	}
}

func TestPromoteRejectsViewerRoleCodeWithoutConsumingIt(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	managerScope := managerAdminScope(t, st, now)
	viewerScope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	if _, err := service.CreateCode(context.Background(), managerScope, CodeRequest{
		DeviceRole: string(store.DeviceRoleViewer),
	}); err != nil {
		t.Fatal(err)
	}

	_, err = service.Promote(context.Background(), viewerScope, PromoteRequest{Code: "PAIR1234"})

	if !errors.Is(err, ErrRoleNotGranted) {
		t.Fatalf("Promote error = %v, want ErrRoleNotGranted", err)
	}
	unchanged, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.DeviceRole != store.DeviceRoleViewer {
		t.Fatalf("DeviceRole = %q, want viewer (promotion rejected)", unchanged.DeviceRole)
	}
	// The code still grants what it was minted for: viewer enrollment.
	if _, err := service.Enroll(context.Background(), EnrollRequest{Code: "PAIR1234", DeviceName: "TV", IsLAN: true}); err != nil {
		t.Fatalf("Enroll after rejected promotion returned error: %v", err)
	}
}

func TestPromoteSharesEnrollThrottleBudget(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	viewerScope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	enableRemoteEnrollment(t, st, now)
	remoteAddr := "203.0.113.50:5000"
	for _, code := range []string{"BAD0001", "BAD0002", "BAD0003", "BAD0004", "BAD0005"} {
		if _, err := service.Promote(context.Background(), viewerScope, PromoteRequest{Code: code, RemoteAddr: remoteAddr}); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("Promote(%q) error = %v, want ErrInvalidCode", code, err)
		}
	}

	// Failed promotions count against the same per-remote budget as enrollment:
	// the promote endpoint is not a second brute-force lane. The
	// budget is now spent, so the next enrollment from that IP is locked out.
	_, err = service.Enroll(context.Background(), EnrollRequest{Code: "BAD0006", DeviceName: "TV", RemoteAddr: "203.0.113.50:6000"})

	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("Enroll error = %v, want ErrThrottled", err)
	}
}

// fakeSPKI is a test SPKIProvider that always offers a fixed pin.
type fakeSPKI struct{ pin string }

func (f fakeSPKI) ServerSPKIPin(context.Context) (string, error) { return f.pin, nil }

// feature:client-cert-pinning — a LAN enrollment captures the
// server's SPKI pin so a native client can trust-on-first-use, but a remote
// (non-LAN) enrollment must NOT silently (re-)pin: only the local-network moment
// establishes the pin, so a relay terminating TLS can never seed an impostor.
func TestEnrollPinsOnLANButNotRemote(t *testing.T) {
	const pin = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

	mint := func(t *testing.T) (*Service, string) {
		t.Helper()
		st := storetest.Open(t)
		now := time.Unix(0, 0).UTC()
		scope := managerAdminScope(t, st, now)
		service := deterministicService(st, now)
		service.SetSPKIProvider(fakeSPKI{pin: pin})
		if _, err := service.CreateCode(context.Background(), scope, CodeRequest{}); err != nil {
			t.Fatal(err)
		}
		return service, "PAIR1234"
	}

	t.Run("LAN enrollment captures the pin", func(t *testing.T) {
		service, code := mint(t)
		resp, err := service.Enroll(context.Background(), EnrollRequest{
			Code:       code,
			DeviceName: "Living Room",
			IsLAN:      true,
		})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if resp.ServerKeyPin != pin {
			t.Fatalf("ServerKeyPin = %q, want %q", resp.ServerKeyPin, pin)
		}
	})

	t.Run("remote enrollment does not re-pin", func(t *testing.T) {
		service, code := mint(t)
		enableRemoteEnrollment(t, service.store, time.Unix(0, 0).UTC())
		resp, err := service.Enroll(context.Background(), EnrollRequest{
			Code:       code,
			DeviceName: "Phone over relay",
			IsLAN:      false,
		})
		if err != nil {
			t.Fatalf("Enroll: %v", err)
		}
		if resp.ServerKeyPin != "" {
			t.Fatalf("remote enrollment leaked a pin: %q", resp.ServerKeyPin)
		}
	})
}

func enableRemoteEnrollment(t *testing.T, st *store.Store, now time.Time) {
	t.Helper()
	enabled := true
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, now); err != nil {
		t.Fatal(err)
	}
}

func TestSetupRequiredTracksBootstrapToken(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())

	// No token installed yet: setup is not required.
	if service.SetupRequired() {
		t.Fatal("SetupRequired = true before any token is installed, want false")
	}

	service.SetBootstrapToken("setup-token")
	if !service.SetupRequired() {
		t.Fatal("SetupRequired = false while the token is installed, want true")
	}

	// A successful Bootstrap consumes the token; setup is no longer required.
	if _, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:      "setup-token",
		DeviceName: "Browser",
	}); err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if service.SetupRequired() {
		t.Fatal("SetupRequired = true after the token is consumed, want false")
	}
}

func TestBootstrapThrottleUsesAuthBackoff(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	service := deterministicService(st, now)
	service.SetBootstrapToken("setup-token")

	for i := 0; i < enrollBackoff.FreeAttempts; i++ {
		_, err := service.Bootstrap(context.Background(), SetupRequest{
			Token:      "wrong-token",
			DeviceName: "Browser",
		})
		if !errors.Is(err, ErrSetupTokenInvalid) {
			t.Fatalf("Bootstrap attempt %d error = %v, want ErrSetupTokenInvalid", i, err)
		}
	}

	_, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:      "wrong-token",
		DeviceName: "Browser",
	})
	if !errors.Is(err, ErrThrottled) {
		t.Fatalf("Bootstrap after failures error = %v, want ErrThrottled", err)
	}
}

func TestBootstrapRetriesAfterSetupWriteFailure(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	ctx := context.Background()
	profile, err := st.CreateProfile(ctx, "Existing", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := st.EnrollDevice(ctx, store.Device{
		ID:         "device-new",
		Name:       "Existing",
		Role:       store.DeviceRoleViewer,
		SecretHash: store.HashDeviceSecret("existing-secret"),
	}, store.Session{
		ID:        "existing-session",
		DeviceID:  "device-new",
		ProfileID: profile.ID,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed duplicate device: %v", err)
	}

	service := NewService(st, func() time.Time { return now })
	service.generateSecret = func() (string, error) { return "device-secret-new", nil }
	ids := []string{"device-new", "session-new", "device-retry", "session-retry"}
	service.generateID = func(string) (string, error) {
		next := ids[0]
		ids = ids[1:]
		return next, nil
	}
	service.SetBootstrapToken("setup-token")

	if _, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser"}); err == nil {
		t.Fatal("Bootstrap with duplicate device succeeded, want setup write failure")
	}
	resp, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser Retry"})
	if err != nil {
		t.Fatalf("Bootstrap retry returned error: %v", err)
	}
	if resp.DeviceID != "device-retry" || resp.SessionID != "session-retry" {
		t.Fatalf("retry response = %+v, want device-retry/session-retry", resp)
	}
	if _, err := service.Bootstrap(ctx, SetupRequest{Token: "setup-token", DeviceName: "Browser Third"}); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("Bootstrap after successful retry = %v, want ErrSetupTokenInvalid", err)
	}
}

func TestBootstrapRejectsConcurrentDoubleSubmit(t *testing.T) {
	st := storetest.Open(t)
	now := time.Unix(0, 0).UTC()
	service := NewService(st, func() time.Time { return now })
	service.generateSecret = func() (string, error) { return "device-secret-new", nil }
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	blocked := false
	counts := map[string]int{}
	service.generateID = func(prefix string) (string, error) {
		mu.Lock()
		if !blocked {
			blocked = true
			close(started)
			mu.Unlock()
			<-release
			mu.Lock()
		}
		counts[prefix]++
		suffix := "new"
		if counts[prefix] > 1 {
			suffix = "retry"
		}
		mu.Unlock()
		return prefix + "-" + suffix, nil
	}
	service.SetBootstrapToken("setup-token")

	errc := make(chan error, 1)
	go func() {
		_, err := service.Bootstrap(context.Background(), SetupRequest{Token: "setup-token", DeviceName: "Browser"})
		errc <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first Bootstrap did not reach setup work")
	}
	if _, err := service.Bootstrap(context.Background(), SetupRequest{Token: "setup-token", DeviceName: "Browser Again"}); !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("concurrent Bootstrap = %v, want ErrSetupTokenInvalid", err)
	}
	close(release)
	if err := <-errc; err != nil {
		t.Fatalf("first Bootstrap returned error: %v", err)
	}
}

func TestBootstrapSetsAdminPassword(t *testing.T) {
	// feature:web-setup-admin-password
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	service.SetBootstrapToken("setup-token")

	if set, err := st.AdminPasswordSet(context.Background()); err != nil || set {
		t.Fatalf("AdminPasswordSet before bootstrap = %v, %v; want false, nil", set, err)
	}

	resp, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:         "setup-token",
		DeviceName:    "Browser",
		AdminPassword: "hunter2-correct-horse",
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if resp.SessionID != "session-new" {
		t.Fatalf("SessionID = %q, want session-new", resp.SessionID)
	}

	if err := st.VerifyAdminPassword(context.Background(), "hunter2-correct-horse"); err != nil {
		t.Fatalf("VerifyAdminPassword with the set password = %v, want nil", err)
	}
	if err := st.VerifyAdminPassword(context.Background(), "wrong"); !errors.Is(err, store.ErrAdminPasswordMismatch) {
		t.Fatalf("VerifyAdminPassword with wrong password = %v, want ErrAdminPasswordMismatch", err)
	}
}

// TestBootstrapDesignatesOwnerProfile pins ADR-0007 §2/§4 first-run: the
// bootstrap Profile is designated the single owner, so the admin gate has an
// owner to bind to from the first session. feature:admin-session
func TestBootstrapDesignatesOwnerProfile(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	service.SetBootstrapToken("setup-token")

	// No owner exists before first-run completes.
	if designated, err := st.OwnerDesignated(context.Background()); err != nil || designated {
		t.Fatalf("OwnerDesignated before bootstrap = (%v, %v), want (false, nil)", designated, err)
	}

	resp, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:         "setup-token",
		DeviceName:    "Browser",
		ProfileName:   "Owner",
		AdminPassword: "hunter2-correct-horse",
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if resp.ProfileID == "" {
		t.Fatal("Bootstrap returned an empty ProfileID")
	}

	// The bootstrap Profile is the owner, and it is the only one.
	if owned, err := st.ProfileIsOwner(context.Background(), store.ProfileID(resp.ProfileID)); err != nil || !owned {
		t.Fatalf("bootstrap profile ProfileIsOwner = (%v, %v), want (true, nil)", owned, err)
	}
	if designated, err := st.OwnerDesignated(context.Background()); err != nil || !designated {
		t.Fatalf("OwnerDesignated after bootstrap = (%v, %v), want (true, nil)", designated, err)
	}
}

func TestBootstrapInvalidTokenSetsNoAdminPassword(t *testing.T) {
	// feature:web-setup-admin-password
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	service.SetBootstrapToken("setup-token")

	_, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:         "wrong-token",
		DeviceName:    "Browser",
		AdminPassword: "should-not-be-set",
	})
	if !errors.Is(err, ErrSetupTokenInvalid) {
		t.Fatalf("Bootstrap with invalid token = %v, want ErrSetupTokenInvalid", err)
	}

	if set, err := st.AdminPasswordSet(context.Background()); err != nil || set {
		t.Fatalf("AdminPasswordSet after invalid-token bootstrap = %v, %v; want false, nil", set, err)
	}
}

func TestBootstrapEmptyAdminPasswordLeavesFlowUnchanged(t *testing.T) {
	// feature:web-setup-admin-password
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	service.SetBootstrapToken("setup-token")

	resp, err := service.Bootstrap(context.Background(), SetupRequest{
		Token:      "setup-token",
		DeviceName: "Browser",
	})
	if err != nil {
		t.Fatalf("Bootstrap returned error: %v", err)
	}
	if resp.SessionID != "session-new" || resp.DeviceID != "device-new" {
		t.Fatalf("Bootstrap response = %+v, want device-new/session-new", resp)
	}

	if set, err := st.AdminPasswordSet(context.Background()); err != nil || set {
		t.Fatalf("AdminPasswordSet after empty-password bootstrap = %v, %v; want false, nil", set, err)
	}
}

func deterministicService(st *store.Store, now time.Time) *Service {
	service := NewService(st, func() time.Time { return now })
	service.generateCode = func() (string, error) { return "PAIR1234", nil }
	service.generateSecret = func() (string, error) { return "device-secret-new", nil }
	ids := []string{"device-new", "session-new"}
	service.generateID = func(_ string) (string, error) {
		next := ids[0]
		ids = ids[1:]
		return next, nil
	}
	return service
}

func managerAdminScope(t *testing.T, st *store.Store, now time.Time) auth.Scope {
	t.Helper()
	if err := st.SetSessionAdminUntil(context.Background(), "session-manager", now.Add(time.Hour)); err != nil {
		t.Fatalf("SetSessionAdminUntil: %v", err)
	}
	authService := auth.NewServiceWithClock(st, func() time.Time { return now })
	scope, err := authService.Scope(context.Background(), "session-manager")
	if err != nil {
		t.Fatal(err)
	}
	if !scope.Admin {
		t.Fatal("manager scope is not admin")
	}
	return scope
}
