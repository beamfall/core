package ffmpeg

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg/ffmpegtest"
)

func TestParseKeyframeCSV(t *testing.T) {
	// An MP4 with a 1/12800 time base, a 7s GOP and B-frames: only K packets
	// count, a discarded keyframe is skipped, and a packet without pts falls
	// back to its dts.
	out := strings.Join([]string{
		"packet,0,-512,K__",
		"packet,1024,-256,___",
		"packet,89600,89088,K__",
		"packet,90112,89344,___",
		"packet,N/A,179200,K__",
		"packet,200000,200000,KD_",
		"stream,1/12800",
		"format,0.000000,21.000000",
		"",
	}, "\n")
	got, err := ParseKeyframeCSV([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, 7 * time.Second, 14 * time.Second}
	if !slices.Equal(got.Keyframes, want) {
		t.Fatalf("keyframes = %v, want %v", got.Keyframes, want)
	}
	if got.Duration != 21*time.Second {
		t.Fatalf("duration = %v, want 21s", got.Duration)
	}
}

func TestParseKeyframeCSVRoundsUpAndSubtractsContainerStart(t *testing.T) {
	// 1/90000: pts 630001 is 7.000011111s, which must round up to 7.000012s so
	// an -ss at that value lands on this keyframe, not the previous one. The
	// container starts at 1.4s (MPEG-TS style), which -ss counts from.
	out := "packet,126000,126000,K_\npacket,756001,756001,K_\nstream,1/90000\nformat,1.400000,30.500000\n"
	got, err := ParseKeyframeCSV([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, 7*time.Second + 12*time.Microsecond}
	if !slices.Equal(got.Keyframes, want) {
		t.Fatalf("keyframes = %v, want %v", got.Keyframes, want)
	}
	if got.Duration != 30500*time.Millisecond {
		t.Fatalf("duration = %v", got.Duration)
	}
}

func TestKeyframeParserHandlesLinesSplitAcrossWrites(t *testing.T) {
	out := "packet,0,0,K__\npacket,7000,7000,K__\nstream,1/1000\nformat,0.000000,10.023000"
	p := &keyframeParser{}
	for i := 0; i < len(out); i += 5 {
		end := min(i+5, len(out))
		if _, err := p.Write([]byte(out[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.index()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Keyframes, []time.Duration{0, 7 * time.Second}) || got.Duration != 10023*time.Millisecond {
		t.Fatalf("index = %+v", got)
	}
}

func TestParseKeyframeCSVRejectsIncompleteOutput(t *testing.T) {
	for name, out := range map[string]string{
		"no video stream": "format,0.000000,10.000000\n",
		"no duration":     "packet,0,0,K_\nstream,1/1000\nformat,0.000000,N/A\n",
		"no keyframes":    "packet,0,0,__\nstream,1/1000\nformat,0.000000,10.000000\n",
		"bad time base":   "packet,0,0,K_\nstream,0/0\nformat,0.000000,10.000000\n",
		"bad timestamp":   "packet,x,0,K_\nstream,1/1000\nformat,0.000000,10.000000\n",
	} {
		if _, err := ParseKeyframeCSV([]byte(out)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestFormatMicros(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                      "0.000000",
		7 * time.Second:                        "7.000000",
		1800*time.Second + 12*time.Microsecond: "1800.000012",
		-1500 * time.Millisecond:               "-1.500000",
	} {
		if got := formatMicros(d); got != want {
			t.Errorf("formatMicros(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestStartHLSKeyframeSegmentsRefusesReencode(t *testing.T) {
	if _, err := New().StartHLSKeyframeSegments(context.Background(), "/media/a.mkv", t.TempDir(), 0, 0, Rendition{}); err == nil {
		t.Fatal("re-encode rendition accepted for keyframe-aligned segmenting")
	}
}

// TestProbeKeyframesReadsRealGOP probes real 7s-GOP files in the two container
// time bases that matter (MKV 1/1000, MP4 1/12800) and checks every keyframe.
func TestProbeKeyframesReadsRealGOP(t *testing.T) {
	ffmpegtest.Require(t)
	dir := t.TempDir()
	want := []time.Duration{0, 7 * time.Second, 14 * time.Second, 21 * time.Second, 28 * time.Second}
	for _, ext := range []string{"mkv", "mp4"} {
		src := filepath.Join(dir, "gop7."+ext)
		gen := exec.Command("ffmpeg", "-v", "error",
			"-f", "lavfi", "-i", "testsrc=size=160x90:rate=25:duration=30",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-g", "175", "-keyint_min", "175", "-sc_threshold", "0",
			"-y", src)
		if out, err := gen.CombinedOutput(); err != nil {
			t.Fatalf("make %s fixture: %v\n%s", ext, err, out)
		}
		got, err := New().ProbeKeyframes(context.Background(), src)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if !slices.Equal(got.Keyframes, want) {
			t.Fatalf("%s keyframes = %v, want %v", ext, got.Keyframes, want)
		}
		if got.Duration < 30*time.Second || got.Duration > 30*time.Second+40*time.Millisecond {
			t.Fatalf("%s duration = %v, want 30s within a frame", ext, got.Duration)
		}
	}
}
