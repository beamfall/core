// Package ffmpegtest is a per-test guard for I/O tests that shell out to
// ffmpeg/ffprobe. Call Require(t) at the top of such a test: it fails loud
// (t.Fatalf, never t.Skip) when the binaries are absent, because the I/O gate
// hard-requires them. Being a per-test guard rather than a tagged TestMain, it
// also protects the untagged `go test ./...` run without collisions.
package ffmpegtest

import (
	"os/exec"
	"testing"
)

// Require fails the test unless both ffmpeg and ffprobe are on PATH.
func Require(t testing.TB) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s required for this I/O test but not on PATH; run `make io-doctor`", bin)
		}
	}
}
