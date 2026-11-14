//go:build io_e2e

package ioe2e

import (
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
)

// io:ffmpeg-probe-keyframes
func TestProbeKeyframesJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")

	index, err := r.ProbeKeyframes(ctx(t), src)
	if err != nil {
		t.Fatalf("ProbeKeyframes(%s): %v", src, err)
	}
	if len(index.Keyframes) == 0 {
		t.Fatalf("ProbeKeyframes(%s): no keyframes", src)
	}
	if index.Duration <= 0 {
		t.Fatalf("ProbeKeyframes(%s): duration %v, want > 0", src, index.Duration)
	}
	for i := 1; i < len(index.Keyframes); i++ {
		if index.Keyframes[i] <= index.Keyframes[i-1] {
			t.Fatalf("keyframes not strictly increasing at %d: %v", i, index.Keyframes)
		}
	}
}

// io:ffmpeg-start-hls-keyframe-segments
func TestStartHLSKeyframeSegmentsJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()

	proc, err := r.StartHLSKeyframeSegments(ctx(t), src, dir, 0, 0, ffmpeg.Rendition{CopyVideo: true})
	if err != nil {
		t.Fatalf("StartHLSKeyframeSegments(%s): %v", src, err)
	}
	defer proc.Stop()

	waitForSegments(t, dir)
	_, segs := hlsArtifacts(t, dir, "live.m3u8")
	if len(segs) == 0 {
		t.Fatalf("no .m4s segments in %s after polling", dir)
	}
	requireNonEmptyFile(t, segs[0])
}
