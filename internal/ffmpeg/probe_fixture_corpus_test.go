package ffmpeg

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fixtureManifest struct {
	FFmpegVersion string                 `json:"ffmpeg_version"`
	GeneratedAt   time.Time              `json:"generated_at"`
	Fixtures      []fixtureManifestEntry `json:"fixtures"`
}

type fixtureManifestEntry struct {
	Path          string   `json:"path"`
	SHA256        string   `json:"sha256"`
	Mode          string   `json:"mode"`
	SynthesisArgs []string `json:"synthesis_args"`
}

const expectedPinnedVersion = "7.1.4-Jellyfin"

func TestPinnedVersionAndFixtureManifest(t *testing.T) {
	pin, err := os.ReadFile("ffmpeg.pin")
	if err != nil {
		t.Fatal(err)
	}
	if string(pin) != expectedPinnedVersion+"\n" {
		t.Fatalf("ffmpeg.pin = %q, want canonical %q", pin, expectedPinnedVersion+"\n")
	}
	if got := PinnedVersion(); got != expectedPinnedVersion {
		t.Fatalf("PinnedVersion() = %q, want %q", got, expectedPinnedVersion)
	}

	data, err := os.ReadFile("testdata/probe/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FFmpegVersion != PinnedVersion() {
		t.Fatalf("manifest FFmpeg version %q does not match pin %q", manifest.FFmpegVersion, PinnedVersion())
	}
	if manifest.GeneratedAt.IsZero() {
		t.Fatal("manifest has no generation timestamp")
	}
	wantFiles, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Fixtures) != len(wantFiles) {
		t.Fatalf("manifest has %d fixtures, want %d", len(manifest.Fixtures), len(wantFiles))
	}
	seen := make(map[string]bool, len(manifest.Fixtures))
	for _, fixture := range manifest.Fixtures {
		if fixture.Path != filepath.Base(fixture.Path) || seen[fixture.Path] {
			t.Fatalf("unsafe or duplicate fixture path %q", fixture.Path)
		}
		seen[fixture.Path] = true
		if fixture.Mode != "fabricated" || len(fixture.SynthesisArgs) != 0 {
			t.Fatalf("fixture %q has dishonest provenance: mode=%q args=%v", fixture.Path, fixture.Mode, fixture.SynthesisArgs)
		}
		data, err := os.ReadFile(filepath.Join("testdata", fixture.Path))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != fixture.SHA256 {
			t.Fatalf("fixture %q hash %q does not match manifest %q", fixture.Path, got, fixture.SHA256)
		}
	}
	for _, path := range wantFiles {
		if !seen[filepath.Base(path)] {
			t.Fatalf("fixture %q is absent from manifest", path)
		}
	}
}

func TestRunnerVersionMatchesPin(t *testing.T) {
	binDir := t.TempDir()
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nprintf '%s\\n' 'ffmpeg version "+expectedPinnedVersion+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- this is a test-owned executable in a private temporary directory.
	if err := os.Chmod(ffmpegPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := New().Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got != PinnedVersion() {
		t.Fatalf("Runner.Version() = %q, want pin %q", got, PinnedVersion())
	}
}

func TestRegenFixturesRefusesMismatchedFFmpeg(t *testing.T) {
	manifestPath := "testdata/probe/manifest.json"
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	ffmpegPath := filepath.Join(binDir, "ffmpeg")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nprintf '%s\\n' 'ffmpeg version 0-mismatch'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G302 -- this is a test-owned executable in a private temporary directory.
	if err := os.Chmod(ffmpegPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "./tools/regen-fixtures")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "does not match pin") {
		t.Fatalf("regen with mismatched FFmpeg = %v, output %q", err, output)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("mismatched FFmpeg modified the fixture manifest")
	}
}

type probeFixtureCorpusEntry struct {
	ID        string          `json:"id"`
	MediaPath string          `json:"media_path"`
	Expected  fixtureExpected `json:"expected"`
	Probe     json.RawMessage `json:"probe"`
}

type fixtureExpected struct {
	Container      string `json:"container"`
	VideoCodec     string `json:"video_codec"`
	AudioCodec     string `json:"audio_codec"`
	VideoProfile   string `json:"video_profile"`
	BitDepth       int    `json:"bit_depth"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
	HDR10Plus      bool   `json:"hdr10_plus"`
	DoViProfile    int    `json:"dovi_profile"`
	DoViBLCompatID int    `json:"dovi_bl_compat_id"`
	SubtitleCodec  string `json:"subtitle_codec"`
}

func TestProbeMediaFixtureCorpusLoadsInTranscodeHarness(t *testing.T) {
	data, err := os.ReadFile("testdata/probe_media_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus []probeFixtureCorpusEntry
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	if len(corpus) < 12 {
		t.Fatalf("corpus has %d entries, want representative codec/HDR/container coverage", len(corpus))
	}

	seen := make(map[string]bool, len(corpus))
	coverage := map[string]bool{}
	for _, fixture := range corpus {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			if fixture.ID == "" || seen[fixture.ID] {
				t.Fatalf("fixture ID is empty or duplicated: %q", fixture.ID)
			}
			seen[fixture.ID] = true
			got, err := parseProbe(fixture.Probe, fixture.MediaPath)
			if err != nil {
				t.Fatalf("parse committed ffprobe fixture: %v", err)
			}
			assertFixtureProbe(t, got, fixture.Expected)

			coverage["codec:"+got.VideoCodec] = true
			coverage["container:"+got.Container] = true
			for _, stream := range got.Streams {
				if stream.HDR10Plus {
					coverage["hdr:hdr10+"] = true
				}
				if stream.ColorTransfer == "smpte2084" {
					coverage["hdr:hdr10"] = true
				}
				if stream.DoViProfile > 0 {
					coverage["hdr:dv"+strconv.Itoa(stream.DoViProfile)] = true
				}
			}
		})
	}

	for _, required := range []string{
		"codec:h264", "codec:hevc", "codec:av1", "codec:vvc", "codec:vc1", "codec:prores",
		"container:mp4", "container:matroska", "container:webm", "container:mpegts", "container:mov", "container:flac",
		"hdr:hdr10", "hdr:hdr10+", "hdr:dv5", "hdr:dv7", "hdr:dv8",
	} {
		if !coverage[required] {
			t.Errorf("committed probe corpus lacks %s coverage", required)
		}
	}
}

func assertFixtureProbe(t *testing.T, got Probe, want fixtureExpected) {
	t.Helper()
	if got.Container != want.Container || got.VideoCodec != want.VideoCodec || got.AudioCodec != want.AudioCodec {
		t.Errorf("probe summary = container %q video %q audio %q, want %q %q %q", got.Container, got.VideoCodec, got.AudioCodec, want.Container, want.VideoCodec, want.AudioCodec)
	}
	var video ProbeStream
	var subtitleCodec string
	for _, stream := range got.Streams {
		if stream.Type == "video" && video.Type == "" {
			video = stream
		}
		if stream.Type == "subtitle" && subtitleCodec == "" {
			subtitleCodec = stream.Codec
		}
	}
	if video.Profile != want.VideoProfile || video.BitDepth != want.BitDepth ||
		video.ColorTransfer != want.ColorTransfer || video.ColorPrimaries != want.ColorPrimaries ||
		video.HDR10Plus != want.HDR10Plus || video.DoViProfile != want.DoViProfile ||
		video.DoViBLCompatID != want.DoViBLCompatID || subtitleCodec != want.SubtitleCodec {
		t.Errorf("video/subtitle inventory = %+v / %q, want %+v", video, subtitleCodec, want)
	}
}
