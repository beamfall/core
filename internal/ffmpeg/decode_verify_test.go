package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDecodeVerifyHealthyVsCorrupt is the real-binary acceptance for the
// decode-verify seam: a well-formed file decodes clean (broken=false)
// and a deliberately-truncated copy of the same encode fails decode
// (broken=true). It is skipped when ffmpeg is not installed, mirroring how this
// package's other FFmpeg-dependent behavior is exercised only where a binary is
// present.
func TestDecodeVerifyHealthyVsCorrupt(t *testing.T) {
	// feature:decode-verify
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary decode-verify test")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.mp4")
	// A short, real H.264 clip from the lavfi test source — a file FFmpeg can
	// always decode cleanly.
	gen := exec.Command(ffmpegPath, "-v", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-t", "2", "-y", good)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip (ffmpeg build lacks libx264?): %v\n%s", err, out)
	}

	runner := New()
	broken, err := runner.DecodeVerify(context.Background(), good, 2)
	if err != nil {
		t.Fatalf("DecodeVerify(good): %v", err)
	}
	if broken {
		t.Fatal("DecodeVerify flagged a healthy clip as broken")
	}

	// Truncate the file hard — keep only the first quarter of the bytes. The
	// moov atom / frame data is now incomplete, so the decoder errors out.
	data, err := os.ReadFile(good)
	if err != nil {
		t.Fatalf("read good clip: %v", err)
	}
	truncated := filepath.Join(dir, "truncated.mp4")
	if err := os.WriteFile(truncated, data[:len(data)/4], 0o600); err != nil {
		t.Fatalf("write truncated clip: %v", err)
	}

	broken, err = runner.DecodeVerify(context.Background(), truncated, 2)
	if err != nil {
		t.Fatalf("DecodeVerify(truncated) returned a run error, want a broken verdict: %v", err)
	}
	if !broken {
		t.Fatal("DecodeVerify did not flag a truncated clip as broken")
	}
}

// TestDecodeVerifyMissingBinaryErrors confirms the safety property at the seam:
// when ffmpeg cannot be resolved, DecodeVerify returns an error (never a broken
// verdict), so a host without ffmpeg can never mass-flag a healthy library.
func TestDecodeVerifyMissingBinaryErrors(t *testing.T) {
	// feature:decode-verify
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Setenv("PATH", "") // hide the real binary for this test only
	}
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg still resolvable after clearing PATH; cannot exercise the missing-binary path")
	}
	broken, err := New().DecodeVerify(context.Background(), "/tmp/whatever.mp4", 0)
	if err == nil {
		t.Fatal("DecodeVerify returned nil error with no ffmpeg, want an error")
	}
	if broken {
		t.Fatal("DecodeVerify reported broken=true with no ffmpeg, want false")
	}
}
