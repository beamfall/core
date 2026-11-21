//go:build beamfall_simclock && !beamfall_appstore

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSimClockShiftsByFileDuration(t *testing.T) {
	base := time.Unix(1_800_000_000, 0).UTC()
	fixed := func() time.Time { return base }
	path := filepath.Join(t.TempDir(), "offset")
	t.Setenv("BEAMFALL_SIMCLOCK_FILE", path)
	clock := simClock(fixed)
	if got := clock(); !got.Equal(base) {
		t.Fatalf("missing file: %v; want base", got)
	}
	if err := os.WriteFile(path, []byte("25h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := clock(); !got.Equal(base.Add(25 * time.Hour)) {
		t.Fatalf("25h offset: %v", got)
	}
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := clock(); !got.Equal(base) {
		t.Fatalf("bad offset: %v; want base", got)
	}
	t.Setenv("BEAMFALL_SIMCLOCK_FILE", "")
	if got := simClock(fixed)(); !got.Equal(base) {
		t.Fatalf("unset env: %v; want base", got)
	}
}
