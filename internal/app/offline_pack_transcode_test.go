package app

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
)

type recordingConverter struct{ profiles []ffmpeg.ConvertProfile }

func (r *recordingConverter) Convert(_ context.Context, _, _ string, profile ffmpeg.ConvertProfile) error {
	r.profiles = append(r.profiles, profile)
	return nil
}

func TestOfflinePackTranscoderMapsEachQualityToItsPreset(t *testing.T) {
	runner := &recordingConverter{}
	transcode := offlinePackTranscoder(runner)
	for quality, want := range map[string]string{
		offlinepack.QualityHigh: "pack-1080p", offlinepack.QualityMedium: "pack-720p", offlinepack.QualityLow: "pack-480p",
	} {
		runner.profiles = nil
		if err := transcode(t.Context(), "/src", "/dst", quality, nil); err != nil {
			t.Fatalf("%s: %v", quality, err)
		}
		if len(runner.profiles) != 1 || runner.profiles[0].Name != want {
			t.Fatalf("%s: profiles = %v, want [%s]", quality, runner.profiles, want)
		}
	}
	for _, quality := range []string{offlinepack.QualityOriginal, "", "ultra"} {
		if err := transcode(t.Context(), "/src", "/dst", quality, nil); err == nil {
			t.Fatalf("quality %q: want an error", quality)
		}
	}
}

// TestOfflinePackTranscoderPassesTheSourceVideoSignal pins CORE-1744: the
// primary video stream's HDR signal reaches the preset, so FFmpeg tone-maps
// an HDR source, while an SDR source keeps the plain preset.
func TestOfflinePackTranscoderPassesTheSourceVideoSignal(t *testing.T) {
	preset, _ := ffmpeg.PackProfileByName("pack-720p")
	audio := store.MediaFileStream{Index: 0, Type: "audio", Codec: "aac"}
	for label, tc := range map[string]struct {
		streams []store.MediaFileStream
		want    ffmpeg.ConvertProfile
	}{
		"hdr10":        {[]store.MediaFileStream{audio, {Index: 1, Type: "video", ColorTransfer: "smpte2084"}}, preset.WithSourceVideo("smpte2084", 0)},
		"hlg":          {[]store.MediaFileStream{{Type: "video", ColorTransfer: "arib-std-b67"}}, preset.WithSourceVideo("arib-std-b67", 0)},
		"dolby vision": {[]store.MediaFileStream{{Type: "video", DoViProfile: 8, DoViBLCompatID: 1}}, preset.WithSourceVideo("", 8)},
		"sdr":          {[]store.MediaFileStream{{Type: "video", ColorTransfer: "bt709"}}, preset.WithSourceVideo("bt709", 0)},
		"no inventory": {nil, preset},
	} {
		runner := &recordingConverter{}
		if err := offlinePackTranscoder(runner)(t.Context(), "/src", "/dst", offlinepack.QualityMedium, tc.streams); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if len(runner.profiles) != 1 || runner.profiles[0] != tc.want {
			t.Fatalf("%s: profile = %+v, want %+v", label, runner.profiles, tc.want)
		}
	}
}
