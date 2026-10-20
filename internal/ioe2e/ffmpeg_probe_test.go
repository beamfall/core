//go:build io

package ioe2e

import "testing"

// TestProbeJourney exercises the ffmpeg probe path over real generated media.
//
// io:ffmpeg-probe
func TestProbeJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")

	p, err := r.Probe(ctx(t), src)
	if err != nil {
		t.Fatalf("Probe(%s): %v", src, err)
	}
	if !p.HasVideo {
		t.Errorf("av_basic probed HasVideo=false, want true")
	}
	if p.VideoCodec != "h264" {
		t.Errorf("av_basic VideoCodec=%q, want h264", p.VideoCodec)
	}
	if p.DurationSeconds < 1 {
		t.Errorf("av_basic DurationSeconds=%d, want >=1", p.DurationSeconds)
	}
	if p.VideoWidth != 640 || p.VideoHeight != 360 {
		t.Errorf("av_basic frame size=%dx%d, want 640x360", p.VideoWidth, p.VideoHeight)
	}
}
