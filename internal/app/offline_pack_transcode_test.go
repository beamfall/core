package app

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/encderiv"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/offlinepack"
	"github.com/beamfall/core/internal/store"
)

type recordingConverter struct {
	profiles []ffmpeg.ConvertProfile
	probe    ffmpeg.Probe
	probeErr error
	probed   []string
}

func (r *recordingConverter) Convert(_ context.Context, _, _ string, profile ffmpeg.ConvertProfile) error {
	r.profiles = append(r.profiles, profile)
	return nil
}

func (r *recordingConverter) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	r.probed = append(r.probed, path)
	return r.probe, r.probeErr
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

// TestOfflinePackTranscoderProbesASourceWithoutAnInventory pins CORE-1845: a
// source scanned without a probe has no video stream in its inventory, so the
// transcoder probes it before deciding tone-mapping. An HDR10 source is then
// tone-mapped on its probed stream, embedded cover art is passed over, a source
// with an inventory is not probed again, and a failed probe refuses rather than
// guessing SDR.
func TestOfflinePackTranscoderProbesASourceWithoutAnInventory(t *testing.T) {
	preset, _ := ffmpeg.PackProfileByName("pack-720p")
	hdrProbe := ffmpeg.Probe{Streams: []ffmpeg.ProbeStream{
		{Index: 0, Type: "video", Codec: "mjpeg", AttachedPicture: true},
		{Index: 1, Type: "audio", Codec: "aac"},
		{Index: 2, Type: "video", Codec: "hevc", ColorTransfer: "smpte2084", ColorPrimaries: "bt2020"},
	}}
	audioOnly := []store.MediaFileStream{{Index: 0, Type: "audio", Codec: "aac"}}
	for label, inventory := range map[string][]store.MediaFileStream{"no inventory": nil, "audio rows only": audioOnly} {
		runner := &recordingConverter{probe: hdrProbe}
		if err := offlinePackTranscoder(runner)(t.Context(), "/src", "/dst", offlinepack.QualityMedium, inventory); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if len(runner.probed) != 1 || runner.probed[0] != "/src" {
			t.Fatalf("%s: probed = %v, want one probe of /src", label, runner.probed)
		}
		want := preset.WithSourceVideo("smpte2084", 0).WithSourceVideoStream(2)
		if len(runner.profiles) != 1 || runner.profiles[0] != want {
			t.Fatalf("%s: profile = %+v, want the probed HDR10 stream %+v", label, runner.profiles, want)
		}
	}

	runner := &recordingConverter{probe: hdrProbe}
	sdr := []store.MediaFileStream{{Index: 0, Type: "video", ColorTransfer: "bt709"}}
	if err := offlinePackTranscoder(runner)(t.Context(), "/src", "/dst", offlinepack.QualityMedium, sdr); err != nil {
		t.Fatal(err)
	}
	if len(runner.probed) != 0 {
		t.Fatalf("probed = %v, want no probe for a source with an inventory", runner.probed)
	}

	failing := &recordingConverter{probeErr: errors.New("ffprobe exited 1")}
	if err := offlinePackTranscoder(failing)(t.Context(), "/src", "/dst", offlinepack.QualityMedium, nil); err == nil || len(failing.profiles) != 0 {
		t.Fatalf("failed probe: err = %v, converts = %d; want an error and no convert", err, len(failing.profiles))
	}
}

// TestOfflinePackTranscoderProbesAnUnprobedHDRSourceRealBinary probes a real
// HDR10-tagged clip with no recorded inventory and checks the conversion is
// asked to tone-map that stream. Skipped without ffmpeg.
func TestOfflinePackTranscoderProbesAnUnprobedHDRSourceRealBinary(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	src := filepath.Join(t.TempDir(), "hdr.mkv")
	gen := exec.CommandContext(t.Context(), ffmpegPath, "-v", "error", // #nosec G204 -- resolved test binary, fixed args
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=10:duration=1",
		"-vf", "setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize HDR-tagged clip: %v\n%s", err, out)
	}
	runner := &probingRecorder{Runner: ffmpeg.New()}
	if err := offlinePackTranscoder(runner)(t.Context(), src, src+".mp4", offlinepack.QualityMedium, nil); err != nil {
		t.Fatal(err)
	}
	preset, _ := ffmpeg.PackProfileByName("pack-720p")
	want := preset.WithSourceVideo("smpte2084", 0).WithSourceVideoStream(0)
	if len(runner.profiles) != 1 || runner.profiles[0] != want {
		t.Fatalf("profile = %+v, want the probed HDR10 stream %+v", runner.profiles, want)
	}
}

// probingRecorder probes with the real runner and records the conversion it
// would run.
type probingRecorder struct {
	*ffmpeg.Runner
	profiles []ffmpeg.ConvertProfile
}

func (r *probingRecorder) Convert(_ context.Context, _, _ string, profile ffmpeg.ConvertProfile) error {
	r.profiles = append(r.profiles, profile)
	return nil
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

type recordingPipe struct {
	inputs   []ffmpeg.PipeInput
	profiles []ffmpeg.ConvertProfile
}

func (r *recordingPipe) TranscodeToPipe(_ context.Context, in ffmpeg.PipeInput, profile ffmpeg.ConvertProfile, _ io.Writer) error {
	r.inputs = append(r.inputs, in)
	r.profiles = append(r.profiles, profile)
	return nil
}

// TestOfflinePackPipeTranscoderUsesTheEndpointAndThePresets pins CORE-1742:
// the encrypted-library transcoder hands FFmpeg the loopback endpoint and its
// token, under the same preset and source signal as a plain Library.
func TestOfflinePackPipeTranscoderUsesTheEndpointAndThePresets(t *testing.T) {
	runner := &recordingPipe{}
	transcode := offlinePackPipeTranscoder(runner)
	in := encderiv.Input{URL: "http://127.0.0.1:40000/source/abc", Secret: "abc", Port: 40000}
	streams := []store.MediaFileStream{{Type: "video", ColorTransfer: "smpte2084"}}
	if err := transcode(t.Context(), in, io.Discard, offlinepack.QualityLow, streams); err != nil {
		t.Fatal(err)
	}
	preset, _ := ffmpeg.PackProfileByName("pack-480p")
	if len(runner.inputs) != 1 || runner.inputs[0] != (ffmpeg.PipeInput{URL: in.URL, Secret: in.Secret}) {
		t.Fatalf("inputs = %+v, want the endpoint URL and token", runner.inputs)
	}
	if runner.profiles[0] != preset.WithSourceVideo("smpte2084", 0) {
		t.Fatalf("profile = %+v, want pack-480p with the HDR source signal", runner.profiles[0])
	}
	for _, quality := range []string{offlinepack.QualityOriginal, "", "ultra"} {
		if err := transcode(t.Context(), in, io.Discard, quality, nil); err == nil {
			t.Fatalf("quality %q: want an error", quality)
		}
	}
	if len(runner.inputs) != 1 {
		t.Fatalf("FFmpeg ran %d times, want only the valid quality", len(runner.inputs))
	}
}
