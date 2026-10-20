package pairing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// Remote-enrollment gate. Pairing-code redemption that did not arrive
// on the LAN is refused unless the operator explicitly enables remote
// enrollment; LAN enrollments are always accepted.

func mintViewerCode(t *testing.T, st *store.Store, service *Service) {
	t.Helper()
	scope := managerAdminScope(t, st, time.Unix(0, 0).UTC())
	if _, err := service.CreateCode(context.Background(), scope, CodeRequest{
		DeviceRole: string(store.DeviceRoleViewer),
	}); err != nil {
		t.Fatalf("CreateCode: %v", err)
	}
}

func TestRemoteEnrollmentDisabledRefusesOffLANRedemption(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	mintViewerCode(t, st, service)

	disabled := false
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &disabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	// IsLAN false + remote disabled → same answer as an invalid code.
	_, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "pair-1234",
		DeviceName: "Remote Laptop",
		IsLAN:      false,
	})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("off-LAN enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestRemoteEnrollmentDisabledStillAllowsLANRedemption(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	mintViewerCode(t, st, service)

	disabled := false
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &disabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	// IsLAN true → always allowed, even with remote enrollment disabled.
	enrolled, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "pair-1234",
		DeviceName: "Living Room",
		IsLAN:      true,
	})
	if err != nil {
		t.Fatalf("LAN enroll error = %v, want success", err)
	}
	if enrolled.DeviceID == "" {
		t.Fatal("LAN enroll returned no device id")
	}
}

func TestRemoteEnrollmentDisabledByDefaultRefusesOffLAN(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	mintViewerCode(t, st, service)

	_, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "pair-1234",
		DeviceName: "Remote Laptop",
		IsLAN:      false,
	})
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("default off-LAN enroll error = %v, want ErrInvalidCode", err)
	}
}

func TestRemoteEnrollmentExplicitEnableAllowsOffLAN(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(0, 0).UTC())
	mintViewerCode(t, st, service)

	enabled := true
	if _, err := st.SetConsoleConfig(context.Background(),
		store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &enabled}, time.Unix(0, 0).UTC()); err != nil {
		t.Fatal(err)
	}

	enrolled, err := service.Enroll(context.Background(), EnrollRequest{
		Code:       "pair-1234",
		DeviceName: "Remote Laptop",
		IsLAN:      false,
	})
	if err != nil {
		t.Fatalf("enabled off-LAN enroll error = %v, want success", err)
	}
	if enrolled.DeviceID == "" {
		t.Fatal("enabled off-LAN enroll returned no device id")
	}
}

func TestRemoteEnrollmentDisabledOffLANAnswersMatchInvalidCode(t *testing.T) {
	cases := []struct {
		name        string
		explicitOff bool
	}{
		{"disabled by default", false},
		{"disabled explicitly", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storetest.Open(t)
			service := deterministicService(st, time.Unix(0, 0).UTC())
			mintViewerCode(t, st, service)
			if tc.explicitOff {
				disabled := false
				if _, err := st.SetConsoleConfig(context.Background(),
					store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &disabled}, time.Unix(0, 0).UTC()); err != nil {
					t.Fatal(err)
				}
			}

			// With the toggle off, an off-LAN caller must get exactly the same
			// answer for a garbage code and a valid code — otherwise the
			// distinct response fingerprints the remote-enrollment toggle and
			// oracles code validity.
			_, garbageErr := service.Enroll(context.Background(), EnrollRequest{
				Code:       "WRONG999",
				DeviceName: "Remote Laptop",
				RemoteAddr: "203.0.113.50:5000",
			})
			_, validErr := service.Enroll(context.Background(), EnrollRequest{
				Code:       "pair-1234",
				DeviceName: "Remote Laptop",
				RemoteAddr: "203.0.113.50:5000",
			})
			if !errors.Is(garbageErr, ErrInvalidCode) {
				t.Fatalf("off-LAN garbage-code error = %v, want ErrInvalidCode", garbageErr)
			}
			if !errors.Is(validErr, ErrInvalidCode) {
				t.Fatalf("off-LAN valid-code error = %v, want ErrInvalidCode", validErr)
			}
			if garbageErr != validErr {
				t.Fatalf("off-LAN answers differ: garbage=%v valid=%v; toggle is fingerprintable", garbageErr, validErr)
			}
		})
	}
}
