package playback

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"golang.org/x/sys/unix"
)

// TestPacingLinuxLandlock uses the production shim. Set CORE0022_LINUX_SHIM
// to a Beamfall binary built from this checkout on a Linux host with Landlock.
func TestPacingLinuxLandlock(t *testing.T) {
	shim := os.Getenv("CORE0022_LINUX_SHIM")
	if shim == "" {
		t.Skip("requires Linux with Landlock and CORE0022_LINUX_SHIM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 || abi < 3 {
		t.Fatalf("Landlock ABI = %d, error = %v", abi, errno)
	}
	t.Logf("Linux %s, Landlock ABI %d", runtime.GOARCH, abi)

	probe := t.TempDir()
	fifo := filepath.Join(probe, "gate")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	mv, err := exec.LookPath("mv")
	if err != nil {
		t.Fatal(err)
	}
	// A FIFO rename in the granted directory is denied even though a
	// regular-file rename in the same directory is permitted.
	// #nosec G204 G702 -- opt-in production binary and test-owned paths.
	out, err := exec.CommandContext(ctx, shim, "ffmpeg-shim", "--require-landlock", "--rw", probe, "--", mv, fifo, fifo+".renamed").CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("Permission denied")) {
		t.Fatalf("shim did not deny the FIFO rename: %v\n%s", err, out)
	}
	t.Logf("shim denied FIFO rename: %s", bytes.TrimSpace(out))

	src := filepath.Join(t.TempDir(), "source.mp4")
	// #nosec G204 -- fixed fixture and test-owned path.
	out, err = exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc2=size=320x240:rate=12", "-t", "5", "-c:v", "mpeg4", src).CombinedOutput()
	if err != nil {
		t.Fatalf("generate fixture: %v\n%s", err, out)
	}

	runner := ffmpeg.New()
	runner.SetHardeningShim(shim)
	sessions := NewSessionsInDir(runner, t.TempDir(), nil)
	t.Cleanup(sessions.Close)
	artifact := func(name string) []byte {
		t.Helper()
		path, err := sessions.FilePath(ctx, pacingTestKey, src, 5, 1280, 720, false, "720p/"+name)
		if err != nil {
			t.Fatalf("publish %s through production shim: %v", name, err)
		}
		part, err := os.ReadFile(path) // #nosec G304 -- test-owned playback cache
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("published %s (%d bytes)", name, len(part))
		return part
	}
	fragment := artifact("seg00000.m4s")
	init := artifact(initSegmentName)
	decode := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-xerror", "-i", "pipe:0", "-frames:v", "1", "-f", "framemd5", "-")
	decode.Stdin = io.MultiReader(bytes.NewReader(init), bytes.NewReader(fragment))
	out, err = decode.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("\n0,")) {
		t.Fatalf("decode first published fragment: %v\n%s", err, out)
	}
	t.Logf("decoded first published fragment:\n%s", out)
}
