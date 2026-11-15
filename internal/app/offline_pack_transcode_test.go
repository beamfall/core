package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/offlinepack"
)

type recordingConverter struct{ profiles []string }

func (r *recordingConverter) Convert(_ context.Context, _, _ string, profile ffmpeg.ConvertProfile) error {
	r.profiles = append(r.profiles, profile.Name)
	return nil
}

func TestOfflinePackTranscoderMapsEachQualityToItsPreset(t *testing.T) {
	runner := &recordingConverter{}
	transcode := offlinePackTranscoder(runner)
	for quality, want := range map[string]string{
		offlinepack.QualityHigh: "pack-1080p", offlinepack.QualityMedium: "pack-720p", offlinepack.QualityLow: "pack-480p",
	} {
		runner.profiles = nil
		if err := transcode(t.Context(), "/src", "/dst", quality); err != nil {
			t.Fatalf("%s: %v", quality, err)
		}
		if len(runner.profiles) != 1 || runner.profiles[0] != want {
			t.Fatalf("%s: profiles = %v, want [%s]", quality, runner.profiles, want)
		}
	}
	for _, quality := range []string{offlinepack.QualityOriginal, "", "ultra"} {
		if err := transcode(t.Context(), "/src", "/dst", quality); err == nil {
			t.Fatalf("quality %q: want an error", quality)
		}
	}
}
