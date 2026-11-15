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
		"hdr10":        {[]store.MediaFileStream{audio, {Index: 1, Type: "video", ColorTransfer: "smpte2084"}}, preset.WithSourceVideo("smpte2084", 0).WithSourceVideoStream(1)},
		"hlg":          {[]store.MediaFileStream{{Type: "video", ColorTransfer: "arib-std-b67"}}, preset.WithSourceVideo("arib-std-b67", 0).WithSourceVideoStream(0)},
		"dolby vision": {[]store.MediaFileStream{{Type: "video", DoViProfile: 8, DoViBLCompatID: 1}}, preset.WithSourceVideo("", 8).WithSourceVideoStream(0)},
		"sdr":          {[]store.MediaFileStream{{Type: "video", ColorTransfer: "bt709"}}, preset.WithSourceVideo("bt709", 0).WithSourceVideoStream(0)},
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

// TestOfflinePackTranscoderMapsTheStreamItReadsTheSignalFrom pins CORE-1850:
// with several video streams, the tone-map decision and the mapped stream are
// the same stream. The default-flagged stream wins, so a Dolby Vision profile
// 5 stream ahead of an SDR default encodes the SDR stream untouched instead of
// refusing; with no default flag the first video stream is both read and
// mapped.
func TestOfflinePackTranscoderMapsTheStreamItReadsTheSignalFrom(t *testing.T) {
	preset, _ := ffmpeg.PackProfileByName("pack-720p")
	audio := store.MediaFileStream{Index: 0, Type: "audio", Codec: "aac", Default: true}
	doViP5 := store.MediaFileStream{Index: 1, Type: "video", Codec: "hevc", DoViProfile: 5}
	for label, tc := range map[string]struct {
		streams []store.MediaFileStream
		want    ffmpeg.ConvertProfile
	}{
		"dolby vision p5 first, sdr default": {
			[]store.MediaFileStream{audio, doViP5, {Index: 2, Type: "video", ColorTransfer: "bt709", Default: true}},
			preset.WithSourceVideo("bt709", 0).WithSourceVideoStream(2),
		},
		"sdr first, hdr10 default": {
			[]store.MediaFileStream{audio, {Index: 1, Type: "video", ColorTransfer: "bt709"}, {Index: 2, Type: "video", ColorTransfer: "smpte2084", Default: true}},
			preset.WithSourceVideo("smpte2084", 0).WithSourceVideoStream(2),
		},
		"no default flag": {
			[]store.MediaFileStream{audio, {Index: 1, Type: "video", ColorTransfer: "arib-std-b67"}, {Index: 2, Type: "video", ColorTransfer: "bt709"}},
			preset.WithSourceVideo("arib-std-b67", 0).WithSourceVideoStream(1),
		},
		"dolby vision p5 alone": {
			[]store.MediaFileStream{audio, doViP5},
			preset.WithSourceVideo("", 5).WithSourceVideoStream(1),
		},
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
