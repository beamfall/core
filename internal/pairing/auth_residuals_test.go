package pairing

import (
	"context"
	"errors"
	"testing"
	"time"

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
