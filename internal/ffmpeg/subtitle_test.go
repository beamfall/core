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

// TestExtractSubtitlesToWebVTTExtractsEveryTrackInOnePass proves the batch
// extraction (CORE-2315) writes one WebVTT per requested text stream from a
// single ffmpeg run over a file that carries two muxed subtitle tracks.
func TestExtractSubtitlesToWebVTTExtractsEveryTrackInOnePass(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "first.srt")
	second := filepath.Join(dir, "second.srt")
	if err := os.WriteFile(first, []byte("1\n00:00:00,000 --> 00:00:01,000\nFirst track cue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("1\n00:00:00,000 --> 00:00:01,000\nSecond track cue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "two.mkv")
	cmd := exec.Command(ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, temp paths and fixed args
		"-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10",
		"-i", first, "-i", second,
		"-pix_fmt", "yuv420p", "-c:s", "srt",
		"-map", "0:v", "-map", "1:s", "-map", "2:s",
		"-y", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthesize MKV with two muxed SRT tracks: %v\n%s", err, out)
	}
	outDir := t.TempDir()
	outputs := map[int]string{
		1: filepath.Join(outDir, "1.vtt"),
		2: filepath.Join(outDir, "2.vtt"),
	}
	if err := New().ExtractSubtitlesToWebVTT(context.Background(), src, outputs); err != nil {
		t.Fatalf("ExtractSubtitlesToWebVTT: %v", err)
	}
	for index, want := range map[int]string{1: "First track cue", 2: "Second track cue"} {
		data, err := os.ReadFile(outputs[index])
		if err != nil {
			t.Fatalf("read stream %d: %v", index, err)
		}
		body := string(data)
		if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, want) {
			t.Fatalf("stream %d WebVTT = %q, want header and %q", index, body, want)
		}
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("output dir has %d entries, want only the two published files", len(entries))
	}
}

func TestExtractSubtitlesToWebVTTRejectsUnsafeArguments(t *testing.T) {
	dir := t.TempDir()
	if err := New().ExtractSubtitlesToWebVTT(context.Background(), "in.mkv", map[int]string{-1: filepath.Join(dir, "a.vtt")}); err == nil {
		t.Fatal("negative stream index accepted")
	}
	if err := New().ExtractSubtitlesToWebVTT(context.Background(), "in.mkv", map[int]string{1: "-evil.vtt"}); err == nil {
		t.Fatal("option-shaped destination accepted")
	}
	if err := New().ExtractSubtitlesToWebVTT(context.Background(), "in.mkv", nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}

func TestResolveStyledSubtitleDelivery(t *testing.T) {
	tests := []struct {
		name   string
		codec  string
		engine []string
		want   SubtitleDelivery
	}{
		{"ass against webvtt engine -> burn-in", "ass", []string{"srt", "webvtt"}, SubtitleBurnIn},
		{"ssa against empty engine -> burn-in", "ssa", nil, SubtitleBurnIn},
		{"ass against libass engine -> sidecar", "ass", []string{"webvtt", "ASS"}, SubtitleSidecar},
		{"srt stays sidecar", "subrip", []string{"webvtt"}, SubtitleSidecar},
		{"pgs still burns", "hdmv_pgs_subtitle", []string{"webvtt", "ass"}, SubtitleBurnIn},
		{"pgs against libmpv -> sidecar", "hdmv_pgs_subtitle", []string{"ass", "pgs"}, SubtitleSidecar},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveStyledSubtitleDelivery(tc.codec, tc.engine); got != tc.want {
				t.Fatalf("ResolveStyledSubtitleDelivery(%q, %v) = %v, want %v", tc.codec, tc.engine, got, tc.want)
			}
		})
	}
}

// TestRenditionTextBurnInArgs proves a styled text burn renders the extracted
// script through libass on the picture (no subtitle stream input), keeps the
// scale cap after it, and, after an input seek, shifts the frames onto the
// source timeline around the filter so the script's times line up.
func TestRenditionTextBurnInArgs(t *testing.T) {
	r := Rendition{Height: 720, CopyVideo: true}.WithBurnTextSubtitle(3, "/s/sub's.ass", "/s/fonts")
	if r.CopyVideo {
		t.Fatal("text burn-in must re-encode")
	}
	if idx, ok := r.burnsSubtitle(); !ok || idx != 3 {
		t.Fatalf("burnsSubtitle = %d,%v, want 3,true", idx, ok)
	}
	want := []string{
		"-filter_complex", `[0:v:0]ass=filename=/s/sub\\\'s.ass:fontsdir=/s/fonts,scale=-2:min(ih\,720)[vsub]`,
		"-map", "[vsub]", "-map", "0:a:0?",
	}
	if got := r.args(); !equalArgs(got, want) {
		t.Fatalf("text burn-in args = %q, want %q", got, want)
	}

	seeked := Rendition{}.WithBurnTextSubtitle(3, "/s/sub.ass", "").withBurnSeek("12.5")
	wantSeeked := []string{
		"-filter_complex", "[0:v:0]setpts=PTS+12.5/TB,ass=filename=/s/sub.ass,setpts=PTS-STARTPTS[vsub]",
		"-map", "[vsub]", "-map", "0:a:0?",
	}
	if got := seeked.args(); !equalArgs(got, wantSeeked) {
		t.Fatalf("seeked text burn-in args = %q, want %q", got, wantSeeked)
	}

	segment := strings.Join(hlsSegmentArgs("/m/in.mkv", "/out", 3, 4, Rendition{}.WithBurnTextSubtitle(3, "/s/sub.ass", "")), " ")
	if !strings.Contains(segment, "-ss 12 ") || !strings.Contains(segment, "setpts=PTS+12/TB,ass=filename=/s/sub.ass") {
		t.Fatalf("segment encode does not carry its seek into the libass chain: %s", segment)
	}

	if _, ok := (Rendition{}).WithBurnTextSubtitle(-1, "/s/sub.ass", "").burnsSubtitle(); ok {
		t.Fatal("negative index must not enable text burn-in")
	}
	if (Rendition{}).withBurnSeek("5").burnText != nil {
		t.Fatal("withBurnSeek must not create a text burn")
	}
}

func TestTextBurnConfinementGrantsScriptAndFonts(t *testing.T) {
	r := Rendition{}.WithBurnTextSubtitle(3, "/scratch/s1/burn-subtitle/subtitle.ass", "/scratch/s1/burn-subtitle/fonts")
	conf := dirConfinement("/media/show/ep.mkv", "/scratch/s1/720p").withBurnText(r)
	want := []string{"/media/show", "/scratch/s1/burn-subtitle", "/scratch/s1/burn-subtitle/fonts"}
	if !equalArgs(conf.ReadOnly, want) {
		t.Fatalf("ReadOnly = %v, want %v", conf.ReadOnly, want)
	}
	if plain := dirConfinement("/media/show/ep.mkv", "/out").withBurnText(Rendition{}); !equalArgs(plain.ReadOnly, []string{"/media/show"}) {
		t.Fatalf("plain rendition ReadOnly = %v", plain.ReadOnly)
	}
}
