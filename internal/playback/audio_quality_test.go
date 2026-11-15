package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// hiResProfile direct-plays FLAC up to 96 kHz / 24-bit stereo.
var hiResProfile = CapabilityProfile{
	Client:          "beamfall-test",
	Containers:      []string{"flac", "mp4"},
	AudioCodecs:     []AudioCodecCap{{Codec: "flac", MaxChannels: 2, MaxSampleRate: 96000, MaxBitDepth: 24}, {Codec: "aac", MaxChannels: 2}},
	DownstreamAudio: "stereo",
}

func importAudioOnlyMedia(t *testing.T, st *store.Store, fingerprint string, stream store.MediaFileStream) store.MediaID {
	t.Helper()
	path := filepath.Join(t.TempDir(), fingerprint+".flac")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Synthetic " + fingerprint,
		Container:           "flac",
		Codec:               stream.Codec,
		Class:               store.MediaClassAudio,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 2,
		Streams:             []store.MediaFileStream{stream},
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return media.ID
}

func flacStream(sampleRate, bitDepth int) store.MediaFileStream {
	return store.MediaFileStream{Index: 0, Type: "audio", Codec: "flac", Channels: 2, SampleRate: sampleRate, BitDepth: bitDepth}
}

// TestAudioResolutionCeilings: a source above the declared sample-rate or
// bit-depth ceiling is transcoded, one within them direct-plays losslessly,
// and a source whose rate and depth the probe never recorded is not refused
// on either axis (CORE-1627).
func TestAudioResolutionCeilings(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stream      store.MediaFileStream
		wantDirect  bool
		wantQuality AudioQuality
	}{
		{"within ceilings", flacStream(96000, 24), true, AudioQuality{Codec: "flac", SampleRate: 96000, BitDepth: 24, Lossless: true, Delivery: AudioDeliveryDirect}},
		{"sample rate above ceiling", flacStream(192000, 24), false, AudioQuality{Codec: "flac", SampleRate: 192000, BitDepth: 24, Lossless: true, Delivery: AudioDeliveryTranscode}},
		{"bit depth above ceiling", flacStream(96000, 32), false, AudioQuality{Codec: "flac", SampleRate: 96000, BitDepth: 32, Lossless: true, Delivery: AudioDeliveryTranscode}},
		{"unrecorded rate and depth", flacStream(0, 0), true, AudioQuality{Codec: "flac", Lossless: true, Delivery: AudioDeliveryDirect}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			mediaID := importAudioOnlyMedia(t, st, "fp-hires-"+filepath.Base(t.Name()), tc.stream)
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: hiResProfile})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if got := resp.Decision == "direct-play"; got != tc.wantDirect {
				t.Fatalf("decision = %q (%s), want direct-play %v", resp.Decision, resp.Reason, tc.wantDirect)
			}
			if !tc.wantDirect && resp.Decision != "transcode" {
				t.Fatalf("decision = %q (%s), want transcode", resp.Decision, resp.Reason)
			}
			if resp.AudioQuality == nil || *resp.AudioQuality != tc.wantQuality {
				t.Fatalf("audioQuality = %+v, want %+v", resp.AudioQuality, tc.wantQuality)
			}
		})
	}
}

func TestExceedsAudioResolution(t *testing.T) {
	codecCap := AudioCodecCap{Codec: "flac", MaxSampleRate: 48000, MaxBitDepth: 16}
	for _, tc := range []struct {
		rate, depth int
		want        bool
	}{
		{48000, 16, false},
		{0, 0, false},
		{96000, 16, true},
		{44100, 24, true},
	} {
		stream := store.MediaFileStream{Type: "audio", Codec: "flac", SampleRate: tc.rate, BitDepth: tc.depth}
		if got := exceedsAudioResolution(codecCap, stream); got != tc.want {
			t.Fatalf("exceedsAudioResolution(%d Hz, %d bit) = %v, want %v", tc.rate, tc.depth, got, tc.want)
		}
	}
	if exceedsAudioResolution(AudioCodecCap{Codec: "flac"}, flacStream(384000, 32)) {
		t.Fatal("a cap without ceilings refused a stream")
	}
}

func TestLosslessAudioStream(t *testing.T) {
	for _, tc := range []struct {
		codec, profile string
		want           bool
	}{
		{"flac", "", true},
		{"alac", "", true},
		{"pcm_s24le", "", true},
		{"pcm_mulaw", "", false},
		{"dsd_lsbf", "", true},
		{"truehd", "", true},
		{"dts", "DTS-HD MA", true},
		{"dts", "DTS", false},
		{"aac", "LC", false},
		{"mp3", "", false},
		{"opus", "", false},
	} {
		stream := store.MediaFileStream{Type: "audio", Codec: tc.codec, Profile: tc.profile}
		if got := losslessAudioStream(stream); got != tc.want {
			t.Fatalf("losslessAudioStream(%s %q) = %v, want %v", tc.codec, tc.profile, got, tc.want)
		}
	}
}

// TestAudioQualityDelivery: only the file itself or a live transcode copying
// a passthrough-declared track delivers the source audio bit-for-bit.
func TestAudioQualityDelivery(t *testing.T) {
	truehd := store.MediaFileStream{Index: 1, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48000, BitDepth: 24}
	streams := []store.MediaFileStream{{Index: 0, Type: "video", Codec: "hevc"}, truehd}
	passthrough := CapabilityProfile{DownstreamAudio: "passthrough", AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}}}
	limited := CapabilityProfile{DownstreamAudio: "passthrough", AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8, MaxSampleRate: 44100}}}
	copyAudio := ffmpeg.AudioProfile{Codec: "copy"}
	eac3 := ffmpeg.AudioProfile{Codec: "eac3", Channels: 6}
	for _, tc := range []struct {
		name    string
		choice  StreamChoice
		audio   ffmpeg.AudioProfile
		profile CapabilityProfile
		want    string
	}{
		{"direct play", StreamChoice{ID: "direct-x", Kind: "direct-play"}, eac3, CapabilityProfile{}, AudioDeliveryDirect},
		{"live transcode copying passthrough", StreamChoice{ID: "hls-x", Kind: "hls"}, copyAudio, passthrough, AudioDeliveryDirect},
		{"audio-copy rung copying passthrough", StreamChoice{ID: "audio-copy-x", Kind: "hls"}, copyAudio, passthrough, AudioDeliveryDirect},
		{"copy beyond the passthrough ceiling", StreamChoice{ID: "hls-x", Kind: "hls"}, copyAudio, limited, AudioDeliveryTranscode},
		{"live transcode re-encoding", StreamChoice{ID: "hls-x", Kind: "hls"}, eac3, passthrough, AudioDeliveryTranscode},
		{"precomputed rendition", StreamChoice{ID: "precomputed-x", Kind: "hls"}, copyAudio, passthrough, AudioDeliveryTranscode},
		{"adjusted stream", StreamChoice{ID: "adjusted-x", Kind: "hls"}, copyAudio, passthrough, AudioDeliveryTranscode},
		{"composed picture", StreamChoice{ID: "composed-x", Kind: "hls"}, copyAudio, passthrough, AudioDeliveryTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := audioQualityFor([]StreamChoice{tc.choice}, streams, 1, tc.audio, tc.profile)
			want := AudioQuality{Codec: "truehd", SampleRate: 48000, BitDepth: 24, Lossless: true, Delivery: tc.want}
			if got == nil || *got != want {
				t.Fatalf("audioQualityFor = %+v, want %+v", got, want)
			}
		})
	}
	if !passthroughAudioDeclared(streams, passthrough) || passthroughAudioDeclared(streams, limited) {
		t.Fatal("passthrough must be declared within the ceiling and refused above it")
	}
	if got := audioQualityFor(nil, streams, 1, eac3, passthrough); got != nil {
		t.Fatalf("no streams: audioQuality = %+v, want nil", got)
	}
	if got := audioQualityFor([]StreamChoice{{ID: "direct-x", Kind: "direct-play"}}, streams[:1], -1, eac3, passthrough); got != nil {
		t.Fatalf("no audio: audioQuality = %+v, want nil", got)
	}
}
