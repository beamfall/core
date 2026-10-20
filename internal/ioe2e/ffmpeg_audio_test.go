//go:build io_e2e

package ioe2e

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoudnessMeasureJourney runs the ebur128 loudness pass over real audio and
// asserts a finite, plausibly-negative integrated LUFS (a 440Hz sine is well
// below 0 LUFS). Values are not pinned — ffmpeg version drift moves the exact
// number — only their shape is checked.
//
// io:ffmpeg-loudness-measure
func TestLoudnessMeasureJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("audio_sine")

	lufs, lra, peak, fromTags, err := r.LoudnessMeasure(ctx(t), src)
	if err != nil {
		t.Fatalf("LoudnessMeasure(%s): %v", src, err)
	}
	if fromTags {
		t.Errorf("audio_sine has no loudness tags, want fromTags=false")
	}
	for name, v := range map[string]float64{"integratedLUFS": lufs, "loudnessRangeLU": lra, "truePeakDBTP": peak} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s=%v, want finite", name, v)
		}
	}
	if lufs >= 0 {
		t.Errorf("integratedLUFS=%v, want negative (a tone is below 0 LUFS)", lufs)
	}
}

// TestWaveformAmplitudesJourney measures the per-window RMS envelope of real
// audio. The documented contract is a non-empty envelope of linear magnitudes
// in [0,1] (dBFS → linear, clamped). audio_sine is a steady 440Hz tone, so the
// envelope must be non-empty and every window in range.
//
// io:ffmpeg-waveform-amplitudes
func TestWaveformAmplitudesJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("audio_sine")

	amps, err := r.WaveformAmplitudes(ctx(t), src)
	if err != nil {
		t.Fatalf("WaveformAmplitudes(%s): %v", src, err)
	}
	if len(amps) == 0 {
		t.Fatalf("WaveformAmplitudes returned no windows")
	}
	for i, a := range amps {
		if a < 0 || a > 1 || math.IsNaN(a) {
			t.Errorf("amp[%d]=%v out of [0,1]", i, a)
		}
	}
}

// TestSilenceSpansJourney runs the real silencedetect pass over silence_gaps
// (tone 0-1s, silence 1.0-3.5s, tone 3.5-4.5s). The 2.5s quiet region is above
// the detector's 2.0s floor (silenceMinDurationSec, ADR-0052 §2.4(a)), so the
// detector must return exactly one span overlapping the known gap.
//
// io:ffmpeg-silence-spans
func TestSilenceSpansJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("silence_gaps")

	spans, err := r.SilenceSpans(ctx(t), src)
	if err != nil {
		t.Fatalf("SilenceSpans(%s): %v", src, err)
	}
	if len(spans) == 0 {
		t.Fatalf("SilenceSpans found no spans; want the 1.0-3.5s gap detected")
	}
	const tol = 0.3
	var overlapsGap bool
	for _, s := range spans {
		start, end := s[0], s[1]
		if end < start {
			t.Errorf("span end %v < start %v", end, start)
		}
		if start < -tol || end > 4.5+tol {
			t.Errorf("span %v outside the 4.5s clip", s)
		}
		if start <= 3.5+tol && end >= 1.0-tol {
			overlapsGap = true
		}
	}
	if !overlapsGap {
		t.Errorf("no detected span overlaps the known 1.0-3.5s gap: %v", spans)
	}
}

// TestAudioFingerprintJourney computes the coarse per-window loudness signature.
// audio_sine is ~2s, so windowCount=8 yields up to 8 one-second windows (fewer
// when the source is shorter than the requested count); values are [0,1].
//
// io:ffmpeg-audio-fingerprint
func TestAudioFingerprintJourney(t *testing.T) {
	r := newRunner(t)
	src := fixtures(t).Path("audio_sine")

	sig, err := r.AudioFingerprint(ctx(t), src, 8)
	if err != nil {
		t.Fatalf("AudioFingerprint(%s): %v", src, err)
	}
	if len(sig) == 0 || len(sig) > 8 {
		t.Fatalf("AudioFingerprint len=%d, want 1..8 (capped at windowCount)", len(sig))
	}
	for i, v := range sig {
		if v < 0 || v > 1 || math.IsNaN(v) {
			t.Errorf("sig[%d]=%v out of [0,1]", i, v)
		}
	}
}

// TestChaptersJourney reads the embedded chapter list. av_basic has no chapters,
// so the no-chapter path (nil error, empty slice) is the asserted contract. As a
// bonus it synthesizes a 2-chapter file via -f ffmetadata and asserts the
// chapters parse back.
//
// io:ffmpeg-chapters
func TestChaptersJourney(t *testing.T) {
	r := newRunner(t)

	// No-chapter path: the common, non-failing case.
	plain := fixtures(t).Path("av_basic")
	chapters, err := r.Chapters(ctx(t), plain)
	if err != nil {
		t.Fatalf("Chapters(%s): %v", plain, err)
	}
	if len(chapters) != 0 {
		t.Errorf("av_basic Chapters len=%d, want 0", len(chapters))
	}

	// Bonus: synthesize a chaptered file and assert the chapters round-trip.
	chaptered := synthesizeChapteredFile(t)
	got, err := r.Chapters(ctx(t), chaptered)
	if err != nil {
		t.Fatalf("Chapters(chaptered): %v", err)
	}
	if len(got) < 1 {
		t.Fatalf("synthesized file Chapters len=%d, want >=1", len(got))
	}
	if got[0].Title == "" {
		t.Errorf("first chapter has empty title, want a parsed title")
	}
}

// synthesizeChapteredFile builds a short MP4 carrying two embedded chapters via
// an ffmetadata sidecar, returning its path under t.TempDir().
func synthesizeChapteredFile(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	dir := t.TempDir()
	meta := filepath.Join(dir, "chapters.ffmeta")
	// ffmetadata chapters use a 1/1000s timebase by default.
	const body = ";FFMETADATA1\n" +
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=Intro\n\n" +
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\ntitle=Body\n"
	if err := os.WriteFile(meta, []byte(body), 0o600); err != nil {
		t.Fatalf("write ffmetadata: %v", err)
	}
	dst := filepath.Join(dir, "chaptered.mp4")
	cmd := exec.Command(ffmpegPath,
		"-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10",
		"-f", "ffmetadata", "-i", meta,
		"-map_metadata", "1",
		"-pix_fmt", "yuv420p",
		"-y", dst,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize chaptered file: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return dst
}

// TestExtractSubtitleToWebVTTJourney synthesizes a clip with a muxed SRT
// subtitle (mirroring internal/ffmpeg/subtitle_test.go), extracts stream 1 to a
// WebVTT sidecar, and asserts the sidecar is non-empty and carries the WEBVTT
// header. No subtitle fixture exists, so the source is built inline.
//
// io:ffmpeg-extract-subtitle-to-web-vtt
func TestExtractSubtitleToWebVTTJourney(t *testing.T) {
	r := newRunner(t)
	src := synthesizeSubtitledMKV(t)
	dst := filepath.Join(t.TempDir(), "out.vtt")

	// The muxed subtitle is the second stream (video=0, subtitle=1).
	if err := r.ExtractSubtitleToWebVTT(ctx(t), src, 1, dst); err != nil {
		t.Fatalf("ExtractSubtitleToWebVTT(%s): %v", src, err)
	}
	requireNonEmptyFile(t, dst)

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "WEBVTT") {
		t.Fatalf("sidecar missing WEBVTT header:\n%s", string(data))
	}
}

// synthesizeSubtitledMKV builds a short MKV with one video stream and one muxed
// SRT subtitle track (the incantation from internal/ffmpeg/subtitle_test.go).
func synthesizeSubtitledMKV(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "sub.srt")
	const srtBody = "1\n00:00:00,000 --> 00:00:01,000\nHello sidecar\n\n" +
		"2\n00:00:01,000 --> 00:00:02,000\nSecond cue\n"
	if err := os.WriteFile(srt, []byte(srtBody), 0o600); err != nil {
		t.Fatalf("write srt: %v", err)
	}
	mkv := filepath.Join(dir, "src.mkv")
	cmd := exec.Command(ffmpegPath,
		"-v", "error",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10",
		"-i", srt,
		"-pix_fmt", "yuv420p",
		"-c:s", "srt",
		"-map", "0:v", "-map", "1:s",
		"-y", mkv,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize subtitled MKV: %v\n%s", err, strings.TrimSpace(string(out)))
	}
	return mkv
}
