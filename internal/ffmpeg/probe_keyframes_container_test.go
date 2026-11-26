package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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

// TestProbeKeyframesCountsFromTheContainerStart pins the keyframe index to
// ffprobe's reading when a Matroska container starts away from zero
// (CORE-3506). A track's CodecDelay moves the start below zero under FFmpeg 8
// (Opus and primed AAC) and the index counts from that start, not from the
// absolute cue times, so the cue-only fast path must leave such files to ffprobe.
func TestProbeKeyframesCountsFromTheContainerStart(t *testing.T) {
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name, shift, audio string
	}{
		{"codec delay below zero", "0", "libopus"},
		{"start above zero", "0.5", "aac"},
	} {
		src := filepath.Join(dir, tc.name+".mkv")
		out, err := exec.Command("ffmpeg", "-v", "error", "-y", // #nosec G204 -- fixed argv, temp paths
			"-itsoffset", tc.shift, "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24:duration=12",
			"-itsoffset", tc.shift, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=12",
			"-c:v", "libx264", "-preset", "ultrafast", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
			"-c:a", tc.audio, src).CombinedOutput()
		if err != nil {
			t.Skipf("%s fixture unavailable: %v\n%s", tc.name, err, out)
		}
		csv, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", // #nosec G204 -- fixed argv, temp path
			"-show_entries", "packet=pts,dts,flags:stream=time_base:format=start_time,duration",
			"-of", "csv=p=1", "--", src).Output()
		if err != nil {
			t.Fatal(err)
		}
		want, err := ParseKeyframeCSV(csv)
		if err != nil {
			t.Fatal(err)
		}
		got, err := New().ProbeKeyframes(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Keyframes) != len(want.Keyframes) {
			t.Fatalf("%s: fast %v, ffprobe %v", tc.name, got.Keyframes, want.Keyframes)
		}
		for i := range want.Keyframes {
			if d := got.Keyframes[i] - want.Keyframes[i]; d < -time.Millisecond || d > time.Millisecond {
				t.Fatalf("%s: fast %v, ffprobe %v", tc.name, got.Keyframes, want.Keyframes)
			}
		}
	}
}
