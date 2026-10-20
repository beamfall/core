package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeSRTMKV synthesizes a short MKV with one video stream and one muxed SRT
// subtitle track, or skips when ffmpeg is unavailable (binaries resolve
// lazily, so a build host without ffmpeg is expected).
func makeSRTMKV(t *testing.T) string {
	t.Helper()
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "sub.srt")
	const srtBody = "1\n00:00:00,000 --> 00:00:01,000\nHello sidecar\n\n" +
		"2\n00:00:01,000 --> 00:00:02,000\nSecond cue\n"
	if err := os.WriteFile(srt, []byte(srtBody), 0o600); err != nil {
		t.Fatal(err)
	}
	mkv := filepath.Join(dir, "src.mkv")
	cmd := exec.Command(ffmpegPath,
		"-v", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration=2:size=320x240:rate=10",
		"-i", srt,
		"-pix_fmt", "yuv420p",
		"-c:s", "srt",
		"-map", "0:v",
		"-map", "1:s",
		"-y", mkv,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize MKV with muxed SRT: %v\n%s", err, out)
	}
	return mkv
}

// TestExtractSubtitleToWebVTT proves a muxed SRT subtitle (the §5 text-subtitle
// case) extracts and converts to a WebVTT sidecar: the output carries the
// "WEBVTT" header and the source cue text.
func TestExtractSubtitleToWebVTT(t *testing.T) {
	src := makeSRTMKV(t)
	// The muxed subtitle is the second stream (video=0, subtitle=1).
	dst := filepath.Join(t.TempDir(), "out.vtt")
	if err := New().ExtractSubtitleToWebVTT(context.Background(), src, 1, dst); err != nil {
		t.Fatalf("ExtractSubtitleToWebVTT: %v", err)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	body := string(data)
	if !strings.HasPrefix(strings.TrimSpace(body), "WEBVTT") {
		t.Fatalf("sidecar missing WEBVTT header:\n%s", body)
	}
	if !strings.Contains(body, "Hello sidecar") || !strings.Contains(body, "Second cue") {
		t.Fatalf("sidecar missing source cue text:\n%s", body)
	}
}

// TestExtractSubtitleToWebVTTRejectsNegativeIndex guards the cheap precondition
// without needing ffmpeg.
func TestExtractSubtitleToWebVTTRejectsNegativeIndex(t *testing.T) {
	err := New().ExtractSubtitleToWebVTT(context.Background(), "/media/x.mkv", -1, "/tmp/out.vtt")
	if err == nil {
		t.Fatal("expected error for negative stream index")
	}
}

// TestResolveSubtitleDelivery covers the §5 branch-selection matrix: a text
// subtitle always takes the sidecar; a PGS bitmap subtitle against an engine
// that cannot render bitmap subs takes burn-in; the same PGS against an engine
// that lists "pgs" stays a sidecar (libmpv); an unknown codec is treated as
// text (sidecar).
func TestResolveSubtitleDelivery(t *testing.T) {
	tests := []struct {
		name   string
		codec  string
		engine []string
		want   SubtitleDelivery
	}{
		{"srt text -> sidecar", "subrip", []string{"webvtt"}, SubtitleSidecar},
		{"ass text -> sidecar", "ass", []string{"webvtt"}, SubtitleSidecar},
		{"pgs against non-libmpv -> burn-in", "hdmv_pgs_subtitle", []string{"srt", "webvtt"}, SubtitleBurnIn},
		{"pgs against empty engine -> burn-in", "hdmv_pgs_subtitle", nil, SubtitleBurnIn},
		{"pgs against libmpv (lists pgs) -> sidecar", "hdmv_pgs_subtitle", []string{"srt", "ass", "pgs"}, SubtitleSidecar},
		{"vobsub against non-renderer -> burn-in", "dvd_subtitle", []string{"webvtt"}, SubtitleBurnIn},
		{"unknown codec -> sidecar", "mystery_subs", []string{"webvtt"}, SubtitleSidecar},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSubtitleDelivery(tc.codec, tc.engine); got != tc.want {
				t.Fatalf("ResolveSubtitleDelivery(%q, %v) = %v, want %v", tc.codec, tc.engine, got, tc.want)
			}
		})
	}
}

// TestRenditionBurnInArgs proves the PGS burn-in branch composites the bitmap
// subtitle via a -filter_complex overlay onto the video stream, forces a
// re-encode (never -c:v copy), maps the composited output plus audio, and folds
// the honest-ABR scale into the filter graph. It must not break the existing
// -c:v copy or plain -vf scale paths.
func TestRenditionBurnInArgs(t *testing.T) {
	// Burn-in forces re-encode even if CopyVideo was set.
	r := Rendition{CopyVideo: true}.WithBurnSubtitle(2)
	if r.CopyVideo {
		t.Fatal("WithBurnSubtitle must drop CopyVideo (cannot overlay onto a copied stream)")
	}
	if got, want := r.videoArgs(), []string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p"}; !equalArgs(got, want) {
		t.Fatalf("burn-in videoArgs = %v, want libx264 re-encode %v", got, want)
	}

	// No scale: overlay the bitmap subtitle stream (index 2) onto [0:v:0].
	got := r.args()
	want := []string{
		"-filter_complex", "[0:v:0][0:2]overlay[vsub]",
		"-map", "[vsub]", "-map", "0:a:0?",
	}
	if !equalArgs(got, want) {
		t.Fatalf("burn-in args = %v, want %v", got, want)
	}

	// With scale + bitrate cap: scale folds into the complex graph, cap rides along.
	scaled := Rendition{Height: 720, VideoKbps: 4000}.WithBurnSubtitle(2)
	gotScaled := scaled.args()
	wantScaled := []string{
		"-filter_complex", "[0:v:0][0:2]overlay[vo];[vo]scale=-2:min(ih\\,720)[vsub]",
		"-map", "[vsub]", "-map", "0:a:0?",
		"-maxrate", "4000k", "-bufsize", "8000k",
	}
	if !equalArgs(gotScaled, wantScaled) {
		t.Fatalf("scaled burn-in args = %v, want %v", gotScaled, wantScaled)
	}

	hdr := Rendition{Height: 720, ColorTransfer: "smpte2084"}.WithBurnSubtitle(2)
	gotHDR := hdr.args()
	wantHDR := []string{
		"-filter_complex", "[0:v:0]tonemapx=tonemap=bt2390:desat=0:peak=1000:t=bt709:m=bt709:p=bt709:range=tv[vtonemap];[vtonemap][0:2]overlay[vo];[vo]scale=-2:min(ih\\,720),format=yuv420p[vsub]",
		"-map", "[vsub]", "-map", "0:a:0?",
	}
	if !equalArgs(gotHDR, wantHDR) {
		t.Fatalf("hdr burn-in args = %v, want %v", gotHDR, wantHDR)
	}

	// Negative index is a no-op: stays the plain (non-burn-in) rendition.
	noop := Rendition{Height: 720}.WithBurnSubtitle(-1)
	if _, ok := noop.burnsSubtitle(); ok {
		t.Fatal("negative index must not enable burn-in")
	}
	if got, want := noop.args(), []string{"-vf", "scale=-2:min(ih\\,720)"}; !equalArgs(got, want) {
		t.Fatalf("noop args = %v, want plain scale %v", got, want)
	}
}

// TestRenditionStreamIndexZeroBurnIn proves stream index 0 is a real, selectable
// burn-in target (the +1 encoding keeps the zero Rendition burn-in-free while
// still allowing index 0).
func TestRenditionStreamIndexZeroBurnIn(t *testing.T) {
	zero := Rendition{}
	if _, ok := zero.burnsSubtitle(); ok {
		t.Fatal("zero Rendition must not burn a subtitle")
	}
	r := Rendition{}.WithBurnSubtitle(0)
	idx, ok := r.burnsSubtitle()
	if !ok || idx != 0 {
		t.Fatalf("WithBurnSubtitle(0) -> (%d,%v), want (0,true)", idx, ok)
	}
}
