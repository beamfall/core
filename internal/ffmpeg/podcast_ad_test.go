package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const audioAdMetadataFixture = `frame:0 pts:0 pts_time:0
lavfi.astats.Overall.RMS_level=-21.5
lavfi.aspectralstats.1.centroid=900
lavfi.aspectralstats.1.flatness=0.2
lavfi.aspectralstats.1.rolloff=1400
frame:1 pts:8192 pts_time:1
lavfi.astats.Overall.RMS_level=-18
lavfi.aspectralstats.1.centroid=1200
lavfi.aspectralstats.1.flatness=0.4
lavfi.aspectralstats.1.rolloff=1800
`

func TestParseAudioAdFeatureMetadataAssociatesFrames(t *testing.T) {
	got, err := parseAudioAdFeatureMetadata(audioAdMetadataFixture, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != AudioAdFeatureSetVersion || got.WindowSeconds != 1 || len(got.Frames) != 2 {
		t.Fatalf("feature set = %+v", got)
	}
	if got.Frames[0].StartSeconds != 0 || got.Frames[0].RMSDBFS != -21.5 ||
		got.Frames[0].SpectralCentroidHz != 900 || got.Frames[0].SpectralFlatness != 0.2 ||
		got.Frames[0].SpectralRolloffHz != 1400 {
		t.Fatalf("frame 0 = %+v", got.Frames[0])
	}
	if got.Frames[1].StartSeconds != 1 || got.Frames[1].SpectralCentroidHz != 1200 {
		t.Fatalf("frame 1 = %+v", got.Frames[1])
	}
}

func TestParseAudioAdFeatureMetadataNormalizesSilence(t *testing.T) {
	fixture := strings.ReplaceAll(audioAdMetadataFixture, "-21.5", "-inf")
	fixture = strings.ReplaceAll(fixture, "900", "nan")
	fixture = strings.ReplaceAll(fixture, "0.2", "nan")
	fixture = strings.ReplaceAll(fixture, "1400", "nan")
	got, err := parseAudioAdFeatureMetadata(fixture, 2)
	if err != nil {
		t.Fatal(err)
	}
	frame := got.Frames[0]
	if frame.RMSDBFS != audioAdSilenceRMSDBFS ||
		frame.SpectralCentroidHz != 0 || frame.SpectralFlatness != 0 || frame.SpectralRolloffHz != 0 {
		t.Fatalf("silent frame = %+v", frame)
	}
	for _, value := range []float64{frame.SpectralCentroidHz, frame.SpectralFlatness, frame.SpectralRolloffHz} {
		if !finite(value) {
			t.Fatalf("non-finite value leaked into emitted feature set: %+v", frame)
		}
	}
}

func TestParseAudioAdFeatureMetadataFailsClosed(t *testing.T) {
	tests := map[string]string{
		"missing metric": strings.Replace(audioAdMetadataFixture,
			"lavfi.aspectralstats.1.flatness=0.2\n", "", 1),
		"duplicate metric": strings.Replace(audioAdMetadataFixture,
			"lavfi.aspectralstats.1.flatness=0.2\n",
			"lavfi.aspectralstats.1.flatness=0.2\nlavfi.aspectralstats.1.flatness=0.3\n", 1),
		"gapped frame":         strings.Replace(audioAdMetadataFixture, "frame:1", "frame:2", 1),
		"gapped timestamp":     strings.Replace(audioAdMetadataFixture, "pts_time:1", "pts_time:1.1", 1),
		"non-finite RMS":       strings.Replace(audioAdMetadataFixture, "-21.5", "nan", 1),
		"RMS above full scale": strings.Replace(audioAdMetadataFixture, "-21.5", "0.1", 1),
		"RMS below feature floor": strings.Replace(audioAdMetadataFixture,
			"-21.5", "-120.1", 1),
		"non-finite spectral": strings.Replace(audioAdMetadataFixture, "900", "nan", 1),
		"infinite spectral":   strings.Replace(audioAdMetadataFixture, "900", "inf", 1),
		"invalid flatness":    strings.Replace(audioAdMetadataFixture, "0.2", "1.2", 1),
		"silent frame malformed spectral": strings.Replace(
			strings.ReplaceAll(audioAdMetadataFixture, "-21.5", "-inf"), "900", "not-a-number", 1),
		"silent frame infinite spectral": strings.Replace(
			strings.ReplaceAll(audioAdMetadataFixture, "-21.5", "-inf"), "900", "inf", 1),
	}
	for name, fixture := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAudioAdFeatureMetadata(fixture, 2); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if _, err := parseAudioAdFeatureMetadata(audioAdMetadataFixture, 1); err == nil {
		t.Fatal("frame bound was not enforced")
	}
}

func TestAudioAdFeaturesUsesHardenedReadOnlyCommand(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "episode.m4a")
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	shimPath := filepath.Join(dir, "shim")
	argsPath := filepath.Join(dir, "args")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil { //nolint:gosec // shim must be executable; lives in t.TempDir
		t.Fatal(err)
	}
	shim := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\nprintf '%%b' %q\n",
		argsPath, audioAdMetadataFixture[:strings.Index(audioAdMetadataFixture, "frame:1")])
	if err := os.WriteFile(shimPath, []byte(shim), 0o700); err != nil { //nolint:gosec // shim must be executable; lives in t.TempDir
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	runner := New()
	runner.SetHardeningShim(shimPath)
	got, err := runner.AudioAdFeatures(context.Background(), src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Frames) != 1 {
		t.Fatalf("frames = %d", len(got.Frames))
	}
	args, err := os.ReadFile(argsPath) //nolint:gosec // test-owned path inside t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--ro\n" + dir, "--\n" + ffmpegPath, "-protocol_whitelist", "-f\nnull", os.DevNull} {
		if !strings.Contains(string(args), want) {
			t.Errorf("shim args missing %q:\n%s", want, args)
		}
	}
}

func TestAudioAdFeaturesRejectsUnboundedRequest(t *testing.T) {
	if _, err := New().AudioAdFeatures(context.Background(), "episode.m4a", 0); err == nil {
		t.Fatal("expected max-window error")
	}
}

func TestAudioAdFeaturesRealFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	src := filepath.Join(t.TempDir(), "tone.wav")
	cmd := exec.Command(ffmpegPath, //nolint:gosec // test-owned shim path inside t.TempDir
		"-v", "error",
		"-f", "lavfi",
		"-i", "sine=frequency=1000:sample_rate=8192:duration=2",
		"-y", src,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate fixture: %v: %s", err, output)
	}
	got, err := New().AudioAdFeatures(context.Background(), src, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(got.Frames))
	}
	for i, frame := range got.Frames {
		values := []float64{
			frame.RMSDBFS,
			frame.SpectralCentroidHz,
			frame.SpectralFlatness,
			frame.SpectralRolloffHz,
		}
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("frame %d = %+v", i, frame)
			}
		}
	}
}
