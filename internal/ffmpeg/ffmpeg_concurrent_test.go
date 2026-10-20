package ffmpeg

import (
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// makeTestSource synthesizes a short H.264 MP4 with ffmpeg's testsrc generator,
// or skips the test when ffmpeg is unavailable (the package resolves binaries
// lazily, so a build host without ffmpeg is expected).
func makeTestSource(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	src := filepath.Join(t.TempDir(), "src.mp4")
	cmd := exec.Command(ffmpegPath,
		"-v", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=320x240:rate=10",
		"-pix_fmt", "yuv420p",
		"-y", src,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize source: %v\n%s", err, out)
	}
	return src
}

// TestFrameGrabConcurrentSameDst proves the temp-file fix: many FrameGrab calls
// racing on one dst each write a distinct temp file, so the published dst is a
// single valid JPEG (no half-written interleave) and no temp files leak.
func TestFrameGrabConcurrentSameDst(t *testing.T) {
	src := makeTestSource(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "frame.jpg")
	r := New()

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.FrameGrab(context.Background(), src, 1.0, 160, dst)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("FrameGrab #%d: %v", i, err)
		}
	}

	// The published file must decode as a complete JPEG — corruption from a
	// concurrent truncate-before-rename would surface as a decode error.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer f.Close()
	if _, err := jpeg.Decode(f); err != nil {
		t.Fatalf("dst is not a valid JPEG (corruption): %v", err)
	}

	// No temp files may remain in the directory after every call returns.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}
