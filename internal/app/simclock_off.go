//go:build !beamfall_simclock

package app

import "time"

// simClock is the identity outside beamfall_simclock builds; see simclock_on.go.
func simClock(base func() time.Time) func() time.Time { return base }
