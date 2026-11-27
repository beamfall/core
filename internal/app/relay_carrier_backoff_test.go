package app

import (
	"testing"
	"time"
)

func TestRelayCarrierBackoffDelayUsesFullJitter(t *testing.T) {
	base := 10 * time.Second
	samples := []int64{
		0,
		int64(base / 2),
		int64(base),
	}
	seen := map[time.Duration]bool{}
	for _, sample := range samples {
		delay := relayCarrierBackoffDelay(base, func(int64) int64 { return sample })
		if delay < 0 || delay > base {
			t.Fatalf("delay %v out of [0, %v] full-jitter envelope", delay, base)
		}
		seen[delay] = true
	}
	if len(seen) != len(samples) {
		t.Fatalf("simulated carriers did not span full jitter envelope: %v", seen)
	}
	if got := relayCarrierBackoffDelay(0, func(int64) int64 { return 0 }); got != 0 {
		t.Fatalf("zero backoff delay = %v, want 0", got)
	}
}

func TestRelayCarrierHealthyRedialAndInvalidHelloDelays(t *testing.T) {
	for _, sample := range []int64{0, int64(relayCarrierHealthyRedialMax / 2), int64(relayCarrierHealthyRedialMax)} {
		d := relayCarrierHealthyRedialDelay(func(int64) int64 { return sample })
		if d < 0 || d > relayCarrierHealthyRedialMax {
			t.Fatalf("healthy redial delay = %v, want [0, %v]", d, relayCarrierHealthyRedialMax)
		}
	}
	span := relayCarrierInvalidHelloMax - relayCarrierInvalidHelloMin
	for _, sample := range []int64{0, int64(span / 2), int64(span)} {
		d := relayCarrierInvalidHelloDelay(func(int64) int64 { return sample })
		if d < relayCarrierInvalidHelloMin || d > relayCarrierInvalidHelloMax {
			t.Fatalf("invalid hello delay = %v, want [%v, %v]", d, relayCarrierInvalidHelloMin, relayCarrierInvalidHelloMax)
		}
	}
}
