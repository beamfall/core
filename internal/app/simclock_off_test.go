//go:build !beamfall_simclock || beamfall_appstore

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSimClockIgnoresFileOutsideSimclockBuilds proves a release or App Store
// build cannot have its clock moved, even with the beamfall_simclock tag set
// alongside beamfall_appstore (CORE-2725).
func TestSimClockIgnoresFileOutsideSimclockBuilds(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	path := filepath.Join(t.TempDir(), "offset")
	if err := os.WriteFile(path, []byte("25h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEAMFALL_SIMCLOCK_FILE", path)
	if got := simClock(func() time.Time { return base })(); !got.Equal(base) {
		t.Fatalf("clock = %v; want %v unshifted", got, base)
	}
}
