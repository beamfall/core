package ffmpeg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertProfileByName(t *testing.T) {
	if _, ok := ConvertProfileByName("h264-mp4"); !ok {
		t.Fatal("h264-mp4 preset missing")
	}
	if _, ok := ConvertProfileByName("nope"); ok {
		t.Fatal("unknown preset reported present")
	}
}

func TestConvertProfileEncodeArgs(t *testing.T) {
	video, _ := ConvertProfileByName("h264-mp4")
	args := video.encodeArgs()
	if !contains(args, "-c:v") || !contains(args, "libx264") || !contains(args, "-c:a") || !contains(args, "aac") {
		t.Fatalf("h264 args = %v", args)
	}
	if contains(args, "-vn") {
		t.Fatal("video preset should not drop video")
	}
	audio, _ := ConvertProfileByName("aac-m4a")
	aargs := audio.encodeArgs()
	if !contains(aargs, "-vn") {
		t.Fatalf("audio-only preset should drop video: %v", aargs)
	}
	if contains(aargs, "-c:v") {
		t.Fatalf("audio-only preset should not set a video codec: %v", aargs)
	}
}

func TestPackProfilesBoundHeightAndBitrate(t *testing.T) {
	cases := map[string][2]string{
		"pack-1080p": {"scale=-2:trunc(min(ih\\,1080)/2)*2", "8000k"},
		"pack-720p":  {"scale=-2:trunc(min(ih\\,720)/2)*2", "4000k"},
		"pack-480p":  {"scale=-2:trunc(min(ih\\,480)/2)*2", "1500k"},
	}
	for name, want := range cases {
		p, ok := PackProfileByName(name)
		if !ok {
			t.Fatalf("%s preset missing", name)
		}
		args := p.encodeArgs()
		for _, pair := range [][2]string{
			{"-c:v", "libx264"}, {"-vf", want[0]}, {"-maxrate", want[1]}, {"-c:a", "aac"},
			{"-ac", "2"}, {"-movflags", "+faststart"},
		} {
			if !containsArgPair(args, pair[0], pair[1]) {
				t.Fatalf("%s args = %v, missing %s %s", name, args, pair[0], pair[1])
			}
		}
		if !contains(args, "-sn") {
			t.Fatalf("%s args = %v, want subtitles dropped", name, args)
		}
	}
	if _, ok := ConvertProfileByName("pack-720p"); ok {
		t.Fatal("pack preset must not be a Library conversion preset")
	}
	if _, ok := PackProfileByName("h264-mp4"); ok {
		t.Fatal("conversion preset must not be a pack preset")
	}
	plain, _ := ConvertProfileByName("h264-mp4")
	for _, flag := range []string{"-vf", "-maxrate", "-b:a", "-ac", "-sn", "-movflags"} {
		if contains(plain.encodeArgs(), flag) {
			t.Fatalf("h264-mp4 args gained %s: %v", flag, plain.encodeArgs())
		}
	}
}

// TestPackProfilesToneMapHDRSources pins CORE-1744: a pack preset re-encoding
// an HDR10, HLG or Dolby Vision source leads its -vf graph with the live
// transcode's tone-map chain, ahead of the scale, while an SDR source gets the
// scale alone.
func TestPackProfilesToneMapHDRSources(t *testing.T) {
	chain := strings.Join(BuildToneMapChain(ToneMapChainRequest{}).Filters, ",")
	if chain == "" {
		t.Fatal("software tone-map chain is empty")
	}
	for _, name := range []string{"pack-1080p", "pack-720p", "pack-480p"} {
		preset, _ := PackProfileByName(name)
		for label, source := range map[string]ConvertProfile{
			"hdr10":        preset.WithSourceVideo("smpte2084", 0),
			"hlg":          preset.WithSourceVideo("arib-std-b67", 0),
			"dolby vision": preset.WithSourceVideo("", 8),
		} {
			vf := argValue(source.encodeArgs(), "-vf")
			if !strings.HasPrefix(vf, chain+",scale=") {
				t.Fatalf("%s %s -vf = %q, want the tone-map chain %q before the scale", name, label, vf, chain)
			}
		}
		for label, source := range map[string]ConvertProfile{"untagged": preset, "bt709": preset.WithSourceVideo("bt709", 0)} {
			vf := argValue(source.encodeArgs(), "-vf")
			if strings.Contains(vf, chain) || !strings.HasPrefix(vf, "scale=") {
				t.Fatalf("%s %s -vf = %q, want the scale and no tone-map", name, label, vf)
			}
		}
	}
	preset, _ := PackProfileByName("pack-720p")
	if vf := argValue(preset.WithSourceVideo("smpte2084", 0).encodeArgs(), "-vf"); vf != chain+",scale=-2:trunc(min(ih\\,720)/2)*2" {
		t.Fatalf("pack-720p hdr10 -vf = %q", vf)
	}
}

// TestConvertRefusesHDRSourceItCannotToneMap: Dolby Vision profile 5 has no
// base layer to tone-map, and an unverified build cannot run the chain, so
// Convert refuses before spawning FFmpeg rather than writing a washed-out file.
func TestConvertRefusesHDRSourceItCannotToneMap(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src.mkv"), filepath.Join(dir, "out.mp4")
	preset, _ := PackProfileByName("pack-720p")
	withToneMapSupport(t, true)
	if err := New().Convert(t.Context(), src, dst, preset.WithSourceVideo("", 5)); !errors.Is(err, ErrToneMapRefused) {
		t.Fatalf("DoVi P5 Convert err = %v, want ErrToneMapRefused", err)
	}
	withToneMapSupport(t, false)
	if err := New().Convert(t.Context(), src, dst, preset.WithSourceVideo("smpte2084", 0)); !errors.Is(err, ErrToneMapRefused) {
		t.Fatalf("unverified-build HDR Convert err = %v, want ErrToneMapRefused", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("refused Convert left %s: %v", dst, err)
	}
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// TestConvertPackProfileRealBinary transcodes a synthesized 720-line clip with
// the 480p pack preset and checks the output is 480 lines, never upscaled
// beyond the source. Skipped when ffmpeg is not installed.
func TestConvertPackProfileRealBinary(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary pack convert test")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed; skipping real-binary pack convert test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.CommandContext(t.Context(), ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-pix_fmt", "yuv420p", "-t", "1", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip (ffmpeg build lacks libx264/aac?): %v\n%s", err, out)
	}
	for name, wantHeight := range map[string]string{"pack-480p": "480", "pack-1080p": "720"} {
		dst := filepath.Join(dir, name+".mp4")
		preset, _ := PackProfileByName(name)
		if err := New().Convert(context.Background(), src, dst, preset); err != nil {
			t.Fatalf("Convert %s: %v", name, err)
		}
		out, err := exec.CommandContext(t.Context(), ffprobePath, "-v", "error", "-select_streams", "v:0", // #nosec G204 -- resolved test binary, fixed args
			"-show_entries", "stream=height", "-of", "csv=p=0", dst).Output()
		if err != nil {
			t.Fatalf("ffprobe %s: %v", name, err)
		}
		if got := strings.TrimSpace(string(out)); got != wantHeight {
			t.Fatalf("%s height = %q, want %s", name, got, wantHeight)
		}
	}
}

// TestConvertRealBinary is the real-binary acceptance for the convert seam: a
// synthesized H.264 MKV clip converts to an MP4, the output exists, and the
// source is left untouched (the store's trash step preserves it later). Skipped
// when ffmpeg is not installed, mirroring this package's other real-binary tests.
func TestConvertRealBinary(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary convert test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.CommandContext(t.Context(), ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-c:v", "libx264", "-c:a", "aac", "-pix_fmt", "yuv420p", "-t", "1", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip (ffmpeg build lacks libx264/aac?): %v\n%s", err, out)
	}

	runner := New()
	dst := filepath.Join(dir, "out.mp4")
	preset, _ := ConvertProfileByName("h264-mp4")
	if err := runner.Convert(context.Background(), src, dst, preset); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if info, err := os.Stat(dst); err != nil || info.Size() == 0 {
		t.Fatalf("converted output missing or empty: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source disturbed by convert: %v", err)
	}
	// No leftover temp files in the output dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("unexpected leftover files in dir: %v", entries)
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestPackProfileMapsTheSourceVideoStream pins CORE-1850: a profile told which
// stream its source signal came from maps that stream (and the first audio
// stream, optionally) ahead of the codec arguments, while a profile with no
// chosen stream, or an audio-only preset, leaves FFmpeg's default choice.
func TestPackProfileMapsTheSourceVideoStream(t *testing.T) {
	preset, _ := PackProfileByName("pack-720p")
	args := preset.WithSourceVideo("bt709", 0).WithSourceVideoStream(2).encodeArgs()
	if len(args) < 4 || strings.Join(args[:4], " ") != "-map 0:2 -map 0:a:0?" {
		t.Fatalf("mapped args = %v, want -map 0:2 -map 0:a:0? first", args)
	}
	if contains(preset.encodeArgs(), "-map") {
		t.Fatalf("unmapped preset args = %v, want no -map", preset.encodeArgs())
	}
	audio, _ := ConvertProfileByName("aac-m4a")
	if contains(audio.WithSourceVideoStream(0).encodeArgs(), "-map") {
		t.Fatalf("audio-only args = %v, want no -map", audio.WithSourceVideoStream(0).encodeArgs())
	}
}

// TestConvertPackProfileEncodesTheMappedStreamRealBinary proves on a real
// FFmpeg that the mapped stream, not FFmpeg's default pick, is encoded: of a
// 480-line first stream and a 720-line second stream (FFmpeg alone prefers
// the larger), mapping each yields its own height. Skipped without ffmpeg.
func TestConvertPackProfileEncodesTheMappedStreamRealBinary(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed; skipping real-binary mapped-stream test")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed; skipping real-binary mapped-stream test")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.CommandContext(t.Context(), ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args
		"-f", "lavfi", "-i", "testsrc=size=640x480:rate=10:duration=1",
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=10:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1",
		"-map", "0:v", "-map", "1:v", "-map", "2:a",
		"-c:v", "libx264", "-c:a", "aac", "-pix_fmt", "yuv420p", "-t", "1", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test clip (ffmpeg build lacks libx264/aac?): %v\n%s", err, out)
	}
	preset, _ := PackProfileByName("pack-1080p")
	for index, wantHeight := range map[int]string{0: "480", 1: "720"} {
		dst := filepath.Join(dir, "out-"+strings.Repeat("v", index+1)+".mp4")
		if err := New().Convert(t.Context(), src, dst, preset.WithSourceVideoStream(index)); err != nil {
			t.Fatalf("Convert stream %d: %v", index, err)
		}
		out, err := exec.CommandContext(t.Context(), ffprobePath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args
			"-show_entries", "stream=codec_type,height", "-of", "csv=p=0", dst).Output()
		if err != nil {
			t.Fatalf("ffprobe stream %d: %v", index, err)
		}
		lines := strings.Fields(strings.TrimSpace(string(out)))
		if len(lines) != 2 || !contains(lines, "video,"+wantHeight) || !contains(lines, "audio") {
			t.Fatalf("stream %d output streams = %q, want one %s-line video and one audio", index, lines, wantHeight)
		}
	}
}
