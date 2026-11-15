package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHLSSegmentArgsMapSelectedAudioStream keeps the seek/restart command line
// on the stream choice the linear one makes (CORE-1774): a selected audio
// stream maps the first video stream and that audio stream, a burned subtitle
// keeps its own overlay map, and no selection leaves FFmpeg's default.
func TestHLSSegmentArgsMapSelectedAudioStream(t *testing.T) {
	selected := 2
	for name, rendition := range map[string]Rendition{
		"reencode":   {Height: 720, AudioStreamIndex: &selected},
		"copy video": {CopyVideo: true, AudioStreamIndex: &selected},
	} {
		args := hlsSegmentArgs("/src/film.mkv", "/tmp/hls", 3, 4, rendition)
		if !containsArgSequence(args, "-map", "0:v:0", "-map", "0:2") {
			t.Fatalf("%s: segment argv = %v, want [-map 0:v:0 -map 0:2]", name, args)
		}
		if linear := hlsArgs("/src/film.mkv", "/tmp/hls", rendition); !containsArgSequence(linear, "-map", "0:v:0", "-map", "0:2") {
			t.Fatalf("%s: linear argv = %v no longer maps the selection", name, linear)
		}
	}

	burn := hlsSegmentArgs("/src/film.mkv", "/tmp/hls", 3, 4, Rendition{AudioStreamIndex: &selected}.WithBurnSubtitle(3))
	if !containsArgSequence(burn, "-map", "[vsub]", "-map", "0:2") || containsArgSequence(burn, "-map", "0:v:0") {
		t.Fatalf("burn-in segment argv = %v, want only [-map [vsub] -map 0:2]", burn)
	}

	if legacy := hlsSegmentArgs("/src/film.mkv", "/tmp/hls", 3, 4, Rendition{Height: 720}); containsArg(legacy, "-map") {
		t.Fatalf("nil audio selection changed the segment argv: %v", legacy)
	}
}

// TestStartHLSSegmentsRestartKeepsSelectedAudioRealEncode restarts a
// two-audio source at a later segment and checks the segments carry the
// selected audio stream, not the one FFmpeg picks by default. The two streams
// differ in sample rate, which the AAC encode keeps.
func TestStartHLSSegmentsRestartKeepsSelectedAudioRealEncode(t *testing.T) {
	requireSegmentFFmpeg(t)
	root := t.TempDir()
	src := filepath.Join(root, "dual-audio.mkv")
	// Stream 1 is the default-flagged stereo 48 kHz track FFmpeg picks on its
	// own; stream 2 is a mono 32 kHz track.
	synthesizeSegmentSource(t, src,
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=25:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000:duration=10",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=32000:duration=10",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ac:a:0", "2", "-ac:a:1", "1",
		"-disposition:a:0", "default", "-disposition:a:1", "0")
	dir := filepath.Join(root, "720p")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	selected := 2
	proc, err := New().StartHLSSegments(context.Background(), src, dir, 2, 2, Rendition{Height: 64, AudioStreamIndex: &selected})
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "seg00002.m4s")); err != nil {
		t.Fatalf("restart did not begin at segment 2: %v", err)
	}
	probe := probeSegmentStreams(t, filepath.Join(dir, "live.m3u8"), "stream=codec_type,sample_rate")
	if !strings.Contains(probe, "codec_type=video") || !strings.Contains(probe, "codec_type=audio|sample_rate=32000") || strings.Contains(probe, "sample_rate=48000") {
		t.Fatalf("restarted segments do not carry the selected 32 kHz audio stream:\n%s", probe)
	}
}

// TestKeyframeSegmentArgsMapProbedVideoStream maps the video stream the
// keyframe probe read (CORE-1735) and the selected audio stream, or the first
// audio stream when none is selected.
func TestKeyframeSegmentArgsMapProbedVideoStream(t *testing.T) {
	plain := keyframeSegmentArgs("/src/film.mkv", "/tmp/hls", 8, 56*time.Second, Rendition{CopyVideo: true})
	if !containsArgSequence(plain, "-map", "0:v:0", "-map", "0:a:0?") {
		t.Fatalf("keyframe argv = %v, want [-map 0:v:0 -map 0:a:0?]", plain)
	}
	selected := 3
	chosen := keyframeSegmentArgs("/src/film.mkv", "/tmp/hls", 8, 56*time.Second, Rendition{CopyVideo: true, AudioStreamIndex: &selected})
	if !containsArgSequence(chosen, "-map", "0:v:0", "-map", "0:3") || containsArg(chosen, "0:a:0?") {
		t.Fatalf("keyframe argv = %v, want [-map 0:v:0 -map 0:3]", chosen)
	}
}

// requireSegmentFFmpeg skips unless ffmpeg and ffprobe are installed.
func requireSegmentFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

// synthesizeSegmentSource writes a lavfi-generated fixture to path.
func synthesizeSegmentSource(t *testing.T, path string, args ...string) {
	t.Helper()
	args = append([]string{"-v", "error"}, args...)
	args = append(args, "-y", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil { // #nosec G204 -- fixed ffmpeg arguments writing a test-owned fixture.
		t.Fatalf("synthesize %s: %v\n%s", filepath.Base(path), err, out)
	}
}

// probeSegmentStreams is ffprobe's compact view of entries in a segmenting
// encode's output playlist.
func probeSegmentStreams(t *testing.T, playlist, entries string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", entries, "-of", "compact", playlist).CombinedOutput() // #nosec G204 -- fixed ffprobe arguments on a test-owned output.
	if err != nil {
		t.Fatalf("ffprobe %s: %v\n%s", playlist, err, out)
	}
	return string(out)
}
