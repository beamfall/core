package racefixture

import (
	"os"
	"sync"
	"testing"
)

// TestDeliberateDataRace is the RED-proving fixture for the `-race` tier.
//
// Default (no env, no -race): skips — keeps `go test ./...` and the in-gate
// `-race -short` subset green.
// Proof (BEAMFALL_PROVE_RACE=1 under `-race`): two goroutines write the same
// int with no synchronization. The detector reports the race and fails the
// package — the intended RED that proves the detector is live. `make race-prove`
// runs exactly this and asserts the failure.
func TestDeliberateDataRace(t *testing.T) {
	if os.Getenv("BEAMFALL_PROVE_RACE") != "1" {
		t.Skip("set BEAMFALL_PROVE_RACE=1 (under -race) to prove the detector is live; see make race-prove")
	}

	var shared int
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				shared++ // unsynchronized RW on shared — the deliberate race
			}
		}()
	}
	wg.Wait()
	_ = shared
}
