package pairing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func refusalReason(err error) auth.RefusalReason {
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal.Reason
	}
	return ""
}

func enrollBrowser(t *testing.T, st *store.Store, deviceID, sessionID string, expires time.Time) {
	t.Helper()
	if err := st.EnrollDevice(context.Background(),
		store.Device{ID: store.DeviceID(deviceID), Name: "Browser", Role: store.DeviceRoleManager, SecretHash: store.HashDeviceSecret("browser-secret")},
		store.Session{ID: store.SessionID(sessionID), DeviceID: store.DeviceID(deviceID), ProfileID: "profile-default", ExpiresAt: expires},
	); err != nil {
		t.Fatal(err)
	}
}

func TestRenewSessionReplacesExpiredSession(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	enrollBrowser(t, st, "device-browser", "session-lapsed", now.Add(-time.Hour))
	service := deterministicService(st, now)
	service.generateID = func(string) (string, error) { return "session-renewed", nil }

	resp, err := service.RenewSession(ctx, RenewRequest{
		DeviceID: "device-browser", DeviceSecret: "browser-secret", OldSessionID: "session-lapsed", RemoteAddr: "192.168.1.5:5000",
	})
	if err != nil {
		t.Fatalf("RenewSession: %v", err)
	}
	if resp.SessionID != "session-renewed" || resp.ProfileID != "profile-default" || resp.DeviceRole != "manager" {
		t.Fatalf("response = %+v", resp)
	}
	if want := now.Add(30 * 24 * time.Hour).Format(time.RFC3339); resp.SessionExpiresAt != want {
		t.Fatalf("SessionExpiresAt = %q, want %q", resp.SessionExpiresAt, want)
	}
	if _, err := st.Session(ctx, "session-lapsed"); err == nil {
		t.Fatal("lapsed session survived renewal")
	}
}

func TestRenewSessionWithoutSessionCookieUsesLatestSession(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	enrollBrowser(t, st, "device-browser", "session-lapsed", now.Add(-time.Hour))
	service := deterministicService(st, now)
	service.generateID = func(string) (string, error) { return "session-renewed", nil }
	resp, err := service.RenewSession(ctx, RenewRequest{DeviceID: "device-browser", DeviceSecret: "browser-secret"})
	if err != nil || resp.ProfileID != "profile-default" {
		t.Fatalf("RenewSession = %+v, %v", resp, err)
	}
}

func TestRenewSessionRefusals(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	enrollBrowser(t, st, "device-browser", "session-lapsed", now.Add(-time.Hour))
	enrollBrowser(t, st, "device-gone", "session-gone", now.Add(time.Hour))
	if err := st.RevokeDevice(ctx, "device-gone"); err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	service.generateID = func(string) (string, error) { return "session-renewed", nil }
	cases := []struct {
		name string
		req  RenewRequest
		want auth.RefusalReason
	}{
		{"missing cookies", RenewRequest{}, auth.RefusalCredentialIncomplete},
		{"missing secret", RenewRequest{DeviceID: "device-browser"}, auth.RefusalCredentialIncomplete},
		{"wrong secret", RenewRequest{DeviceID: "device-browser", DeviceSecret: "guess"}, auth.RefusalDeviceSecretInvalid},
		{"unknown device", RenewRequest{DeviceID: "device-nope", DeviceSecret: "browser-secret"}, auth.RefusalDeviceSecretInvalid},
		{"revoked device", RenewRequest{DeviceID: "device-gone", DeviceSecret: "browser-secret", OldSessionID: "session-gone"}, auth.RefusalDeviceRevoked},
	}
	for _, tc := range cases {
		tc.req.RemoteAddr = "192.168.1.5:5000"
		_, err := service.RenewSession(ctx, tc.req)
		if !errors.Is(err, ErrSessionRenewRefused) || refusalReason(err) != tc.want {
			t.Fatalf("%s: err = %v (reason %q), want refused with %q", tc.name, err, refusalReason(err), tc.want)
		}
	}
	if _, err := st.Session(ctx, "session-renewed"); err == nil {
		t.Fatal("a refused renewal created a session")
	}
}

func TestRenewSessionThrottlesFailuresPerSource(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	enrollBrowser(t, st, "device-browser", "session-lapsed", now.Add(-time.Hour))
	service := deterministicService(st, now)
	service.generateID = func(string) (string, error) { return "session-renewed", nil }
	guess := RenewRequest{DeviceID: "device-browser", DeviceSecret: "guess", RemoteAddr: "203.0.113.5:4000"}
	for i := 0; i < enrollBackoff.FreeAttempts; i++ {
		if _, err := service.RenewSession(ctx, guess); !errors.Is(err, ErrSessionRenewRefused) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	guess.DeviceSecret = "browser-secret"
	if _, err := service.RenewSession(ctx, guess); !errors.Is(err, ErrThrottled) {
		t.Fatalf("throttled source err = %v, want ErrThrottled", err)
	}
	guess.RemoteAddr = "192.168.1.5:5000"
	if _, err := service.RenewSession(ctx, guess); err != nil {
		t.Fatalf("other source err = %v, want success", err)
	}
}

func TestAdminSignInEnrollsManagerOnOwner(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	resp, err := service.AdminSignIn(ctx, AdminSignInRequest{Password: "correct horse", DeviceName: "Laptop", RemoteAddr: "192.168.1.5:5000"})
	if err != nil {
		t.Fatalf("AdminSignIn: %v", err)
	}
	if resp.DeviceRole != "manager" || resp.ProfileID != "profile-default" || resp.DeviceSecret == "" {
		t.Fatalf("response = %+v", resp)
	}
	device, err := st.VerifyDeviceSecret(ctx, store.DeviceID(resp.DeviceID), resp.DeviceSecret)
	if err != nil || device.Role != store.DeviceRoleManager || device.Name != "Laptop" {
		t.Fatalf("device = %+v, %v", device, err)
	}
	session, err := st.Session(ctx, store.SessionID(resp.SessionID))
	if err != nil || !session.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("session = %+v, %v", session, err)
	}
}

func TestAdminSignInRefusesUniformly(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_000_000, 0).UTC()
	req := AdminSignInRequest{Password: "correct horse", DeviceName: "Laptop", RemoteAddr: "192.168.1.5:5000"}

	noPassword := storetest.Open(t)
	if err := noPassword.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	noOwner := storetest.Open(t)
	if err := noOwner.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	wrong := storetest.Open(t)
	if err := wrong.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := wrong.SetAdminPassword(ctx, "another password", now); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]*store.Store{"no password": noPassword, "no owner": noOwner, "wrong password": wrong} {
		_, err := deterministicService(st, now).AdminSignIn(ctx, req)
		if !errors.Is(err, ErrSignInFailed) || err.Error() != "sign-in failed" || refusalReason(err) != auth.RefusalAdminPasswordRejected {
			t.Fatalf("%s: err = %v, want uniform sign-in failure", name, err)
		}
	}
}

func TestAdminSignInThrottlesPerSourceAndServerWide(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	free := auth.AdminBackoff().FreeAttempts
	// Distributed guessing: each source stays under its own budget, but the
	// server-wide lane still locks.
	for i := 0; i < free; i++ {
		addr := "203.0.113." + string(rune('1'+i)) + ":4000"
		if _, err := service.AdminSignIn(ctx, AdminSignInRequest{Password: "guess", DeviceName: "x", RemoteAddr: addr}); !errors.Is(err, ErrSignInFailed) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	_, err := service.AdminSignIn(ctx, AdminSignInRequest{Password: "correct horse", DeviceName: "x", RemoteAddr: "192.168.1.5:5000"})
	if !errors.Is(err, ErrThrottled) || refusalReason(err) != auth.RefusalAdminLocked {
		t.Fatalf("server-wide lane err = %v, want ErrThrottled", err)
	}
}

// TestAdminSignInParallelGuessesCannotOutrunTheLanes: the attempt is counted
// before the KDF runs, so a burst of parallel guesses from one source spends
// exactly the free attempts and every other request is throttled.
func TestAdminSignInParallelGuessesCannotOutrunTheLanes(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword(ctx, "correct horse", now); err != nil {
		t.Fatal(err)
	}
	service := deterministicService(st, now)
	free := auth.AdminBackoff().FreeAttempts
	errs := make([]error, 4*free)
	var start, done sync.WaitGroup
	start.Add(1)
	for i := range errs {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			_, errs[i] = service.AdminSignIn(ctx, AdminSignInRequest{Password: "guess", DeviceName: "x", RemoteAddr: "203.0.113.9:4000"})
		}(i)
	}
	start.Done()
	done.Wait()
	var failed, throttled int
	for i, err := range errs {
		switch {
		case errors.Is(err, ErrSignInFailed):
			failed++
		case errors.Is(err, ErrThrottled):
			throttled++
		default:
			t.Fatalf("attempt %d err = %v", i, err)
		}
	}
	if failed != free || throttled != len(errs)-free {
		t.Fatalf("failed = %d, throttled = %d, want %d and %d", failed, throttled, free, len(errs)-free)
	}
}

func TestCreateOwnerCodeMintsManagerCodeForOwner(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	now := time.Unix(1_000_000, 0).UTC()
	service := deterministicService(st, now)
	if _, err := service.CreateOwnerCode(ctx, ""); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("no owner err = %v, want ErrNoOwner", err)
	}
	if err := st.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateOwnerCode(ctx, "admin"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("bad role err = %v, want ErrInvalidRequest", err)
	}
	code, err := service.CreateOwnerCode(ctx, "")
	if err != nil {
		t.Fatalf("CreateOwnerCode: %v", err)
	}
	if code.Code != "PAIR1234" || code.DeviceRole != "manager" || code.ProfileID != "profile-default" ||
		code.ExpiresAt != now.Add(10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("code = %+v", code)
	}
	enrolled, err := service.Enroll(ctx, EnrollRequest{Code: code.Code, DeviceName: "Browser", IsLAN: true})
	if err != nil || enrolled.DeviceRole != "manager" || enrolled.ProfileID != "profile-default" {
		t.Fatalf("Enroll with host code = %+v, %v", enrolled, err)
	}
}
