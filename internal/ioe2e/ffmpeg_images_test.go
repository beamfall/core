//go:build io_e2e

package ioe2e

import (
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
)

// TestFrameGrabJourney grabs a single scaled frame from real media.
//
// io:ffmpeg-frame-grab
func TestFrameGrabJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dst := filepath.Join(t.TempDir(), "frame.png")

	if err := r.FrameGrab(ctx(t), src, 1.0, 160, dst); err != nil {
		t.Fatalf("FrameGrab(%s): %v", src, err)
	}
	requireNonEmptyFile(t, dst)
}

// TestScaleImageJourney scales a real image (itself a grabbed frame) down to a
// target width. ScaleImage's source must be an image, so the journey first
// grabs a PNG frame from av_basic, then scales that PNG.
//
// io:ffmpeg-scale-image
func TestScaleImageJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()
	frame := filepath.Join(dir, "frame.png")
	if err := r.FrameGrab(ctx(t), src, 1.0, 320, frame); err != nil {
		t.Fatalf("FrameGrab seed: %v", err)
	}

	scaled := filepath.Join(dir, "scaled.png")
	if err := r.ScaleImage(ctx(t), frame, 80, scaled); err != nil {
		t.Fatalf("ScaleImage(%s): %v", frame, err)
	}
	requireNonEmptyFile(t, scaled)
}

// TestSpriteSheetsJourney tiles thumbnails into a small sprite grid.
//
// io:ffmpeg-sprite-sheets
func TestSpriteSheetsJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dir := t.TempDir()
	pattern := filepath.Join(dir, "sprite_%03d.jpg")

	if err := r.SpriteSheets(ctx(t), src, 1, 80, 2, 1, pattern); err != nil {
		t.Fatalf("SpriteSheets(%s): %v", src, err)
	}
	sprites, err := filepath.Glob(filepath.Join(dir, "sprite_*.jpg"))
	if err != nil {
		t.Fatalf("glob sprites: %v", err)
	}
	if len(sprites) == 0 {
		t.Fatalf("SpriteSheets produced no sprite files in %s", dir)
	}
	requireNonEmptyFile(t, sprites[0])
}

// TestSampleClipJourney stitches a short low-FPS preview clip from offsets.
//
// io:ffmpeg-sample-clip
func TestSampleClipJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dst := filepath.Join(t.TempDir(), "sample.mp4")

	if err := r.SampleClip(ctx(t), src, []float64{0.5}, 0.5, 160, 12, dst); err != nil {
		t.Fatalf("SampleClip(%s): %v", src, err)
	}
	requireNonEmptyFile(t, dst)

	p, err := r.Probe(ctx(t), dst)
	if err != nil {
		t.Fatalf("Probe(sample clip): %v", err)
	}
	if !p.HasVideo {
		t.Errorf("sample clip HasVideo=false, want true")
	}
}

// TestConvertJourney transcodes real media to a new container/codec via the
// Conversion Workflow's one-shot FFmpeg seam (POWERTOOLS-1 / apple ADR-0003 §1):
// the output exists, is a valid probe-able MP4 with video, and the source is
// left untouched (the store's trash step preserves it later).
//
// io:ffmpeg-convert
func TestConvertJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")
	dst := filepath.Join(t.TempDir(), "converted.mp4")

	preset, ok := ffmpeg.ConvertProfileByName("h264-mp4")
	if !ok {
		t.Fatal("h264-mp4 preset missing")
	}
	if err := r.Convert(ctx(t), src, dst, preset); err != nil {
		t.Fatalf("Convert(%s): %v", src, err)
	}
	requireNonEmptyFile(t, dst)

	p, err := r.Probe(ctx(t), dst)
	if err != nil {
		t.Fatalf("Probe(converted): %v", err)
	}
	if !p.HasVideo {
		t.Errorf("converted output HasVideo=false, want true")
	}
	requireNonEmptyFile(t, src)
}

// TestDecodeVerifyJourney decodes sample windows of a clean file and reports it
// not broken.
//
// io:ffmpeg-decode-verify
func TestDecodeVerifyJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("av_basic")

	broken, err := r.DecodeVerify(ctx(t), src, 2)
	if err != nil {
		t.Fatalf("DecodeVerify(%s): %v", src, err)
	}
	if broken {
		t.Errorf("clean fixture av_basic reported broken=true, want false")
	}
}
