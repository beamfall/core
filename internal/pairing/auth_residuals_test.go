package pairing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestSetupThrottleIsPerSource pins CORE-0728: the setup throttle has no
// server-wide lane, so wrong tokens from one source never delay the operator's
// correct token from another source.
func TestSetupThrottleIsPerSource(t *testing.T) {
	st := storetest.Open(t)
	service := deterministicService(st, time.Unix(100, 0).UTC())
	service.SetBootstrapToken("setup-token")
	ctx := context.Background()

	wrong := SetupRequest{Token: "wrong-token", DeviceName: "Browser", RemoteAddr: "203.0.113.5:4000"}
	for i := 0; i < enrollBackoff.FreeAttempts; i++ {
		if _, err := service.Bootstrap(ctx, wrong); !errors.Is(err, ErrSetupTokenInvalid) {
			t.Fatalf("wrong-token attempt %d error = %v, want ErrSetupTokenInvalid", i, err)
		}
	}
	if _, err := service.Bootstrap(ctx, wrong); !errors.Is(err, ErrThrottled) {
		t.Fatalf("guessing source error = %v, want ErrThrottled", err)
	}

	if _, err := service.Bootstrap(ctx, SetupRequest{
		Token:      "setup-token",
		DeviceName: "Operator Browser",
		RemoteAddr: "198.51.100.7:5000",
	}); err != nil {
		t.Fatalf("correct token from another source error = %v, want success", err)
	}
}

// TestDemoEnrollmentObeysRemoteEnrollmentPolicy pins CORE-0728: the reusable
// demo grant cannot enroll off-LAN Devices while remote enrollment is disabled,
// and still enrolls once the operator enables it.
func TestDemoEnrollmentObeysRemoteEnrollmentPolicy(t *testing.T) {
	const key = "review-demo-key-123456"
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()
	remote := EnrollRequest{Code: key, DeviceName: "Remote Review Device", RemoteAddr: "203.0.113.9:6000"}

	st := storetest.Open(t)
	service := deterministicService(st, now)
	if err := service.SetDemoEnrollment(DemoEnrollment{Key: key, ProfileID: "profile-managed"}); err != nil {
		t.Fatalf("SetDemoEnrollment: %v", err)
	}
	disabled := false
	if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteEnrollmentEnabled: &disabled}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Enroll(ctx, remote); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("off-LAN demo enroll with remote enrollment disabled error = %v, want ErrInvalidCode", err)
	}
	if _, err := st.Device(ctx, "device-new"); err == nil {
		t.Fatal("refused off-LAN demo enrollment still stored a Device")
	}

	enableRemoteEnrollment(t, st, now)
	enrolled, err := service.Enroll(ctx, remote)
	if err != nil {
		t.Fatalf("off-LAN demo enroll with remote enrollment enabled error = %v, want success", err)
	}
	if enrolled.DeviceRole != string(store.DeviceRoleViewer) || enrolled.ProfileID != "profile-managed" {
		t.Fatalf("enrolled = %+v, want viewer on profile-managed", enrolled)
	}
}
