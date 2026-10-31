package ffmpeg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func argAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func TestComposedHLSArgsGraphics(t *testing.T) {
	args := composedHLSArgs("/lib/song.mp3", "/cache/r/composed", ComposedPicture{GraphicsPath: "/lib/song.cdg"}, Rendition{VideoKbps: 1500})
	if args[0] != "-v" || args[2] != "-i" || args[3] != "/lib/song.cdg" {
		t.Fatalf("picture input must come first: %q", args)
	}
	if got, want := argAfter(args, "-vf"), "scale=1000:720:flags=neighbor,pad=1280:720:(ow-iw)/2:0,fps=30,tpad=stop=-1:stop_mode=clone,format=yuv420p"; got != want {
		t.Fatalf("-vf = %q\nwant %q", got, want)
	}
	for _, want := range [][]string{
		{"-i", "/lib/song.mp3"}, {"-map", "0:v:0"}, {"-map", "1:a:0"}, {"-c:v", "libx264"},
		{"-maxrate", "1500k"}, {"-c:a", "aac"}, {"-hls_segment_type", "fmp4"},
	} {
		if !slices.Contains(args, want[0]) || !containsPair(args, want[0], want[1]) {
			t.Errorf("missing %q %q in %q", want[0], want[1], args)
		}
	}
	if !slices.Contains(args, "-shortest") || args[len(args)-1] != "/cache/r/composed/index.m3u8" {
		t.Fatalf("audio must bound the output and the playlist close the args: %q", args)
	}
}

func TestComposedHLSArgsTimedText(t *testing.T) {
	args := composedHLSArgs("/lib/song.ogg", "/cache/precompute:m1:fp:composed/composed", ComposedPicture{TimedTextPath: "/cache/precompute:m1:fp:composed/composed/picture.ass"}, Rendition{})
	if got := strings.Join(args[2:6], " "); got != "-f lavfi -i color=c=black:s=1280x720:r=30" {
		t.Fatalf("canvas input = %q", got)
	}
	if got, want := argAfter(args, "-vf"), `ass=filename=/cache/precompute\\:m1\\:fp\\:composed/composed/picture.ass,format=yuv420p`; got != want {
		t.Fatalf("-vf = %q\nwant %q", got, want)
	}
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestEscapeFilterValue(t *testing.T) {
	for in, want := range map[string]string{
		"plain/path.ass": "plain/path.ass",
		"a:b":            `a\\:b`,
		"it's":           `it\\\'s`,
		`back\slash`:     `back\\\\slash`,
		"a,b;c[d]":       `a\,b\;c\[d\]`,
	} {
		if got := escapeFilterValue(in); got != want {
			t.Errorf("escapeFilterValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartBackgroundComposedHLSRefusesOutsideConfinement(t *testing.T) {
	src := t.TempDir()
	audio := filepath.Join(src, "song.mp3")
	out := t.TempDir()
	elsewhere := t.TempDir()
	writeFile := func(path string) string {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	writeFile(audio)
	outsideGraphics := writeFile(filepath.Join(elsewhere, "song.cdg"))
	linkedGraphics := filepath.Join(src, "linked.cdg")
	if err := os.Symlink(outsideGraphics, linkedGraphics); err != nil {
		t.Fatal(err)
	}
	outsideText := writeFile(filepath.Join(elsewhere, "picture.ass"))
	cases := map[string]ComposedPicture{
		"graphics elsewhere":         {GraphicsPath: outsideGraphics},
		"graphics symlinked outward": {GraphicsPath: linkedGraphics},
		"timed text outside out dir": {TimedTextPath: outsideText},
	}
	r := New()
	for name, picture := range cases {
		if _, err := r.StartBackgroundComposedHLS(context.Background(), audio, out, picture, Rendition{}); !errors.Is(err, ErrComposedPictureOutsideConfinement) {
			t.Errorf("%s: err = %v, want ErrComposedPictureOutsideConfinement", name, err)
		}
	}
	for name, picture := range map[string]ComposedPicture{
		"neither":   {},
		"both":      {GraphicsPath: outsideGraphics, TimedTextPath: outsideText},
		"flag-like": {GraphicsPath: "-i"},
	} {
		if _, err := r.StartBackgroundComposedHLS(context.Background(), audio, out, picture, Rendition{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := r.StartBackgroundComposedHLS(context.Background(), "-f", out, ComposedPicture{TimedTextPath: outsideText}, Rendition{}); err == nil {
		t.Error("flag-like audio source accepted")
	}
}

// requireComposeCapableFFmpeg skips unless ffmpeg/ffprobe are installed with
// the graphics decoder and the libass filter composed renditions use.
func requireComposeCapableFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	decoders, _ := exec.Command("ffmpeg", "-hide_banner", "-decoders").Output()
	filters, _ := exec.Command("ffmpeg", "-hide_banner", "-filters").Output()
	if !strings.Contains(string(decoders), "cdgraphics") || !strings.Contains(string(filters), " ass ") {
		t.Skip("ffmpeg lacks the cdgraphics decoder or the libass filter")
	}
}

func synthesizeAudio(t *testing.T, path string) {
	t.Helper()
	gen := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=6", "-y", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("synthesize audio: %v\n%s", err, out)
	}
}

// graphicsTrack is a minimal subcode graphics stream: a color table, a screen
// fill, and a row of tiles, then idle packets — a whole number of 24-byte
// packets at 300 per second.
func graphicsTrack(seconds int) []byte {
	packet := func(instruction byte, data ...byte) []byte {
		p := make([]byte, 24)
		p[0], p[1] = 0x09, instruction
		copy(p[4:20], data)
		return p
	}
	var out []byte
	out = append(out, packet(30, 0x00, 0x04, 0x3E, 0x30)...) // color table: dark blue, amber
	out = append(out, packet(1, 0, 0)...)                    // fill the screen with color 0
	for col := byte(10); col < 40; col++ {
		tile := []byte{0, 1, 6, col}
		for row := 0; row < 12; row++ {
			tile = append(tile, 0x2A)
		}
		out = append(out, packet(6, tile...)...)
	}
	for len(out) < seconds*300*24 {
		out = append(out, make([]byte, 24)...)
	}
	return out
}

func probeHLS(t *testing.T, playlist string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,codec_name,width,height",
		"-show_entries", "format=duration", "-of", "compact", playlist).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v\n%s", playlist, err, out)
	}
	return string(out)
}

// io:ffmpeg-start-background-composed-hls
func TestStartBackgroundComposedHLSRealEncode(t *testing.T) {
	requireComposeCapableFFmpeg(t)
	// Directory names carry every filtergraph-special character, so the
	// escaping is proven by FFmpeg itself, not only by string comparison.
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(src, "song.mp3")
	synthesizeAudio(t, audio)
	graphics := filepath.Join(src, "song.cdg")
	if err := os.WriteFile(graphics, graphicsTrack(6), 0o600); err != nil {
		t.Fatal(err)
	}
	const ass = "[Script Info]\nScriptType: v4.00+\nPlayResX: 1280\nPlayResY: 720\n\n[V4+ Styles]\n" +
		"Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		"Style: Default,Sans,64,&H0000B4FF,&H00FFFFFF,&H00000000,&H80000000,-1,0,0,0,100,100,0,0,1,4,2,2,60,60,0,1\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.50,0:00:05.00,Default,,0,0,300,,{\\k50}{\\kf200}Hello {\\kf200}world\n"

	for name, compose := range map[string]func(dir string) ComposedPicture{
		"graphics": func(string) ComposedPicture { return ComposedPicture{GraphicsPath: graphics} },
		"timed text": func(dir string) ComposedPicture {
			path := filepath.Join(dir, "picture.ass")
			if err := os.WriteFile(path, []byte(ass), 0o600); err != nil {
				t.Fatal(err)
			}
			return ComposedPicture{TimedTextPath: path}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(root, "precompute:m1:fp,x;y[z]'q", strings.ReplaceAll(name, " ", "-"))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			proc, err := New().StartBackgroundComposedHLS(context.Background(), audio, dir, compose(dir), Rendition{Height: 720, VideoKbps: 1500, AudioCodec: "aac", AudioChannels: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err := proc.Wait(); err != nil {
				t.Fatalf("encode: %v", err)
			}
			probe := probeHLS(t, filepath.Join(dir, "index.m3u8"))
			for _, want := range []string{"codec_name=h264|codec_type=video|width=1280|height=720", "codec_name=aac|codec_type=audio"} {
				if !strings.Contains(probe, want) {
					t.Errorf("probe missing %q:\n%s", want, probe)
				}
			}
			// The held last frame keeps the picture as long as the audio.
			if !strings.Contains(probe, "duration=6.") && !strings.Contains(probe, "duration=5.9") {
				t.Errorf("duration not bounded by the 6s audio:\n%s", probe)
			}
		})
	}
}
