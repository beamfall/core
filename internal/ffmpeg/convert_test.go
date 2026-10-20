package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConvertProfileByName(t *testing.T) {
	if _, ok := ConvertProfileByName("h264-mp4"); !ok {
		t.Fatal("h264-mp4 preset missing")
	}
	if _, ok := ConvertProfileByName("nope"); ok {
		t.Fatal("unknown preset reported present")
	}
}

func TestConvertProfileEncodeArgs(t *testing.T) {
	video, _ := ConvertProfileByName("h264-mp4")
	args := video.encodeArgs()
	if !contains(args, "-c:v") || !contains(args, "libx264") || !contains(args, "-c:a") || !contains(args, "aac") {
		t.Fatalf("h264 args = %v", args)
	}
	if contains(args, "-vn") {
		t.Fatal("video preset should not drop video")
	}
	audio, _ := ConvertProfileByName("aac-m4a")
	aargs := audio.encodeArgs()
	if !contains(aargs, "-vn") {
		t.Fatalf("audio-only preset should drop video: %v", aargs)
	}
	if contains(aargs, "-c:v") {
		t.Fatalf("audio-only preset should not set a video codec: %v", aargs)
	}
}

// TestConvertRealBinary is the real-binary acceptance for the convert seam: a
// synthesized H.264 MKV clip converts to an MP4, the output exists, and the
// source is left untouched (the store's trash step preserves it later). Skipped
// when ffmpeg is not installed, mirroring this package's other real-binary tests.
func TestConvertRealBinary(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary convert test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command(ffmpegPath, "-v", "error",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-pix_fmt", "yuv420p", "-t", "1", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip (ffmpeg build lacks libx264/aac?): %v\n%s", err, out)
	}

	runner := New()
	dst := filepath.Join(dir, "out.mp4")
	preset, _ := ConvertProfileByName("h264-mp4")
	if err := runner.Convert(context.Background(), src, dst, preset); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if info, err := os.Stat(dst); err != nil || info.Size() == 0 {
		t.Fatalf("converted output missing or empty: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source disturbed by convert: %v", err)
	}
	// No leftover temp files in the output dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("unexpected leftover files in dir: %v", entries)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
