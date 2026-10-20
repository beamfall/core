//go:build io || io_e2e

package ioe2e

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/beamfall/core/internal/apptest"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/ffmpeg/ffmpegtest"
)

// newRunner returns a real ffmpeg.Runner after asserting the toolchain is
// present (fail-loud — these are the slow tier, never silently skipped).
func newRunner(t testing.TB) *ffmpeg.Runner {
	t.Helper()
	ffmpegtest.Require(t)
	return ffmpeg.New()
}

// fixtures returns the standalone generated-media accessor (logical name →
// path + manifest sha256). Run `make fixtures` if missing — it fails loud.
func fixtures(t testing.TB) *apptest.Fixtures {
	t.Helper()
	return apptest.NewFixtures(t)
}

// ctx returns a per-test context bounded by a generous slow-tier deadline so a
// hung ffmpeg never wedges the suite.
func ctx(t testing.TB) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return c
}

// requireNonEmptyFile fails unless path exists and has a non-zero size — the
// structural assertion most ffmpeg output journeys share.
func requireNonEmptyFile(t testing.TB, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected output file %s: %v", path, err)
	}
	if info.Size() == 0 {
		t.Fatalf("output file %s is empty", path)
	}
}
