//go:build ffmpeg

package main

import (
	"context"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
)

// TestFFmpegShimHappyPathRealTranscode is the criterion (c) regression check
// with the REAL shim wired in end to end: it builds the beamfall binary, points
// BEAMFALL_FFMPEG_SHIM at it so internal/ffmpeg re-execs the real shim, then runs
// a genuine FrameGrab through that path and confirms a valid JPEG comes out. The
// confinement must allow exactly the input dir (ro) and output dir (rw); if the
// shim over-restricted, ffmpeg would fail to read or write and this fails.
//
// On darwin (the dev/gate host) this exercises the rlimit + syscall.Exec half;
// landlock is Linux-only and is not enforced here. The test is build-tagged
// `ffmpeg` so it runs only where ffmpeg/ffprobe exist, never in the default
// `make gate`.
func TestFFmpegShimHappyPathRealTranscode(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}

	// Build the beamfall binary to serve as the real hardening shim.
	bin := filepath.Join(t.TempDir(), "beamfall")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beamfall: %v\n%s", err, out)
	}
	t.Setenv(ffmpeg.ShimEnvVar, bin)

	// Synthesize a short source clip in its own directory.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "src.mp4")
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x240:rate=10", "-pix_fmt", "yuv420p", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("synthesize source: %v\n%s", err, out)
	}

	// FrameGrab writes into a distinct output directory — the shim must grant
	// srcDir ro and the output dir rw for this to succeed.
	dst := filepath.Join(t.TempDir(), "frame.jpg")
	if err := ffmpeg.New().FrameGrab(context.Background(), src, 0.5, 160, dst); err != nil {
		t.Fatalf("FrameGrab through shim failed (over-restricted confinement?): %v", err)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open dst: %v", err)
	}
	defer f.Close()
	if _, err := jpeg.Decode(f); err != nil {
		t.Fatalf("dst is not a valid JPEG: %v", err)
	}
}

// TestFFmpegShimComposedTimedText runs a composed timed-text rendition through
// the REAL shim: libass must load fonts through fontconfig from the runtime
// font directories the landlock policy grants read-only, while the encode
// itself stays confined to the audio dir (ro) and the rung dir (rw). A policy
// that hid the fonts would fail the encode or leave the canvas blank, so the
// test also checks that text pixels were drawn.
func TestFFmpegShimComposedTimedText(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	bin := filepath.Join(t.TempDir(), "beamfall")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build beamfall: %v\n%s", err, out)
	}
	t.Setenv(ffmpeg.ShimEnvVar, bin)

	srcDir := t.TempDir()
	audio := filepath.Join(srcDir, "song.mp3")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=3", "-y", audio).CombinedOutput(); err != nil {
		t.Fatalf("synthesize audio: %v\n%s", err, out)
	}
	rungDir := filepath.Join(t.TempDir(), "precompute:m:fp:composed", "composed")
	if err := os.MkdirAll(rungDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ass := filepath.Join(rungDir, "picture.ass")
	doc := "[Script Info]\nScriptType: v4.00+\nPlayResX: 1280\nPlayResY: 720\n\n[V4+ Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		"Style: Default,Sans,120,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,0,0,5,0,0,0,1\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.00,0:00:03.00,Default,,0,0,0,,HELLO\n"
	if err := os.WriteFile(ass, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	proc, err := ffmpeg.New().StartBackgroundComposedHLS(context.Background(), audio, rungDir,
		ffmpeg.ComposedPicture{TimedTextPath: ass}, ffmpeg.Rendition{Height: 720, VideoKbps: 1500})
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("composed encode through shim failed (fonts or confinement?): %v", err)
	}
	// A black canvas has no bright pixels; drawn white text does.
	// The fMP4 init plus its first media segment is a playable MP4.
	var joined []byte
	for _, name := range []string{"init.mp4", "seg00000.m4s"} {
		part, err := os.ReadFile(filepath.Join(rungDir, name))
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, part...)
	}
	sample := filepath.Join(t.TempDir(), "sample.mp4")
	if err := os.WriteFile(sample, joined, 0o600); err != nil {
		t.Fatal(err)
	}
	frame := filepath.Join(t.TempDir(), "frame.png")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-ss", "1", "-i", sample,
		"-frames:v", "1", "-y", frame).CombinedOutput(); err != nil {
		t.Fatalf("extract frame: %v\n%s", err, out)
	}
	f, err := os.Open(frame)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	bright := 0
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y += 2 {
		for x := bounds.Min.X; x < bounds.Max.X; x += 2 {
			if r, g, b, _ := img.At(x, y).RGBA(); r > 0xC000 && g > 0xC000 && b > 0xC000 {
				bright++
			}
		}
	}
	if bright < 500 {
		t.Fatalf("no text drawn under confinement: %d bright pixels", bright)
	}
}
