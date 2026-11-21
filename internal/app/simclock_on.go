//go:build beamfall_simclock && !beamfall_appstore

package app

import (
	"os"
	"strings"
	"time"
)

// simClock shifts the read-path clock by the duration written in the file
// BEAMFALL_SIMCLOCK_FILE names (for example "25h"), read on every call. It
// exists so a recorded run can prove a credential outlives a day without
// waiting one (CORE-2725). Only binaries built with the beamfall_simclock tag
// carry it, and the beamfall_appstore tag compiles it out even if both are set.
func simClock(base func() time.Time) func() time.Time {
	path := os.Getenv("BEAMFALL_SIMCLOCK_FILE")
	if path == "" {
		return base
	}
	return func() time.Time {
		now := base()
		raw, err := os.ReadFile(path) // #nosec G304 G703 -- operator-chosen path in a test-only build
		if err != nil {
			return now
		}
		shift, err := time.ParseDuration(strings.TrimSpace(string(raw)))
		if err != nil {
			return now
		}
		return now.Add(shift)
	}
}
