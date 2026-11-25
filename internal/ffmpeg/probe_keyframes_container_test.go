package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProbeKeyframesFastMatchesFFprobeOnMP4AndMKV(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	r := New()
	dir := t.TempDir()
	for _, ext := range []string{".mp4", ".mkv"} {
		src := filepath.Join(dir, "sample"+ext)
		cmd := r.hardenedCommand(context.Background(), "ffmpeg", nil, []string{dir}, []string{
			"-v", "error", "-y",
			"-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24:duration=12",
			"-c:v", "libx264", "-preset", "ultrafast",
			"-g", "96", "-keyint_min", "96", "-sc_threshold", "0",
			src,
		})
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("synthesize %s: %v\n%s", ext, err, out)
		}
		fastIdx, ok := probeKeyframesFast(src)
		if !ok {
			t.Fatalf("probeKeyframesFast(%s) returned false", ext)
		}
		if len(fastIdx.Keyframes) < 3 {
			t.Fatalf("probeKeyframesFast(%s) keyframes = %v, want >= 3", ext, fastIdx.Keyframes)
		}
		if fastIdx.Keyframes[0] != 0 {
			t.Fatalf("probeKeyframesFast(%s) first keyframe = %v, want 0", ext, fastIdx.Keyframes[0])
		}
		if fastIdx.Duration < 11_900_000_000 {
			t.Fatalf("probeKeyframesFast(%s) duration = %v, want ~12s", ext, fastIdx.Duration)
		}
	}
}
