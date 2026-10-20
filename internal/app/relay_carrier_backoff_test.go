package app

import (
	"errors"
	"testing"
	"time"

	"github.com/beamfall/core/internal/remoteaccess"
)

func TestRelayCarrierBackoffDelayJittersWithinEnvelope(t *testing.T) {
	base := 10 * time.Second
	span := base / 5
	samples := []int64{
		0,
		int64(span),
		int64(2*span) - 1,
	}
	seen := map[time.Duration]bool{}
	for _, sample := range samples {
		delay := relayCarrierBackoffDelay(base, func(int64) int64 { return sample })
		if delay < base-span || delay > base+span {
			t.Fatalf("delay %v out of +/-20%% envelope for %v", delay, base)
		}
		seen[delay] = true
	}
	if len(seen) == 1 {
		t.Fatalf("simulated carriers produced identical delays: %v", seen)
	}
	if got := relayCarrierBackoffDelay(0, func(int64) int64 { return 0 }); got != 0 {
		t.Fatalf("zero backoff delay = %v, want 0", got)
	}
}

func TestRelayCarrierRetryPolicy(t *testing.T) {
	if relayCarrierShouldRetry(remoteaccess.ErrRelayInvalidHello) {
		t.Fatal("invalid hello must stop relay reconnects")
	}
	for _, err := range []error{
		errors.New("relay registration: relay rejected carrier: unauthorized"),
		errors.New("dialing relay: connection refused"),
	} {
		if !relayCarrierShouldRetry(err) {
			t.Fatalf("%q must retain normal reconnect backoff", err)
		}
	}
}
