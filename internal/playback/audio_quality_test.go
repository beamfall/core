package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
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
// a passthrough-declared track, unadjusted, delivers the source audio
// bit-for-bit.
func TestAudioQualityDelivery(t *testing.T) {
	truehd := store.MediaFileStream{Index: 1, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48000, BitDepth: 24}
	streams := []store.MediaFileStream{{Index: 0, Type: "video", Codec: "hevc"}, truehd}
	commentaryFirst := []store.MediaFileStream{{Index: 0, Type: "audio", Codec: "aac", Channels: 2}, truehd}
	passthrough := CapabilityProfile{DownstreamAudio: "passthrough", AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}}}
	limited := CapabilityProfile{DownstreamAudio: "passthrough", AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8, MaxSampleRate: 44100}}}
	copyAudio := ffmpeg.AudioProfile{Codec: "copy"}
	eac3 := ffmpeg.AudioProfile{Codec: "eac3", Channels: 6}
	tempo := ffmpeg.AudioAdjust{Tempo: 1.1}
	for _, tc := range []struct {
		name    string
		choice  StreamChoice
		streams []store.MediaFileStream
		audio   ffmpeg.AudioProfile
		adjust  ffmpeg.AudioAdjust
		profile CapabilityProfile
		want    string
	}{
		{"direct play", StreamChoice{ID: "direct-x", Kind: "direct-play"}, streams, eac3, ffmpeg.AudioAdjust{}, CapabilityProfile{}, AudioDeliveryDirect},
		{"live transcode copying passthrough", StreamChoice{ID: "hls-x", Kind: "hls"}, streams, copyAudio, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryDirect},
		{"audio-copy rung copying passthrough", StreamChoice{ID: "audio-copy-x", Kind: "hls"}, streams, copyAudio, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryDirect},
		{"copy beyond the passthrough ceiling", StreamChoice{ID: "hls-x", Kind: "hls"}, streams, copyAudio, ffmpeg.AudioAdjust{}, limited, AudioDeliveryTranscode},
		{"live transcode re-encoding", StreamChoice{ID: "hls-x", Kind: "hls"}, streams, eac3, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryTranscode},
		{"precomputed rendition", StreamChoice{ID: "precomputed-x", Kind: "hls"}, streams, copyAudio, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryTranscode},
		{"adjusted stream", StreamChoice{ID: "adjusted-x", Kind: "hls"}, streams, copyAudio, tempo, passthrough, AudioDeliveryTranscode},
		{"live composed picture copying audio", StreamChoice{ID: "composed-x", Kind: "hls"}, streams, copyAudio, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryDirect},
		{"adjusted composed picture", StreamChoice{ID: "composed-x", Kind: "hls"}, streams, copyAudio, tempo, passthrough, AudioDeliveryTranscode},
		{"composed picture re-encoding", StreamChoice{ID: "composed-x", Kind: "hls"}, streams, eac3, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryTranscode},
		{"composed picture mapping another track", StreamChoice{ID: "composed-x", Kind: "hls"}, commentaryFirst, copyAudio, ffmpeg.AudioAdjust{}, passthrough, AudioDeliveryTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := audioQualityFor([]StreamChoice{tc.choice}, tc.streams, 1, tc.audio, tc.adjust, tc.profile)
			want := AudioQuality{Codec: "truehd", SampleRate: 48000, BitDepth: 24, Lossless: true, Delivery: tc.want}
			if got == nil || *got != want {
				t.Fatalf("audioQualityFor = %+v, want %+v", got, want)
			}
		})
	}
	if !passthroughAudioDeclared(streams, 1, passthrough) || passthroughAudioDeclared(streams, 1, limited) {
		t.Fatal("passthrough must be declared within the ceiling and refused above it")
	}
	if passthroughAudioDeclared(commentaryFirst, 0, passthrough) {
		t.Fatal("passthrough declared for the chosen AAC track because another track is TrueHD")
	}
	if got := audioQualityFor(nil, streams, 1, eac3, ffmpeg.AudioAdjust{}, passthrough); got != nil {
		t.Fatalf("no streams: audioQuality = %+v, want nil", got)
	}
	if got := audioQualityFor([]StreamChoice{{ID: "direct-x", Kind: "direct-play"}}, streams[:1], -1, eac3, ffmpeg.AudioAdjust{}, passthrough); got != nil {
		t.Fatalf("no audio: audioQuality = %+v, want nil", got)
	}
}

// TestSelectedTrackDecidesPassthroughCeiling: with two TrueHD tracks, the
// passthrough ceiling is checked against the selected track alone. A selected
// 192 kHz track above a 48 kHz ceiling is re-encoded even though the other
// track fits it, and selecting the track that fits copies it (CORE-1627).
func TestSelectedTrackDecidesPassthroughCeiling(t *testing.T) {
	profile := CapabilityProfile{
		Client:          "passthrough-tv",
		Containers:      []string{"mp4"},
		VideoCodecs:     []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
		AudioCodecs:     []AudioCodecCap{{Codec: "truehd", MaxChannels: 8, MaxSampleRate: 48000}, {Codec: "eac3", MaxChannels: 6}},
		DownstreamAudio: "passthrough",
	}
	hiRes := audioStream(1, "truehd", 8)
	hiRes.SampleRate, hiRes.BitDepth, hiRes.Default = 192000, 24, true
	standard := audioStream(2, "truehd", 8)
	standard.SampleRate, standard.BitDepth = 48000, 24
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-selected-ceiling", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""), hiRes, standard,
	})
	for _, tc := range []struct {
		selected     int
		wantCopy     bool
		wantRate     int
		wantDelivery string
	}{
		{1, false, 192000, AudioDeliveryTranscode},
		{2, true, 48000, AudioDeliveryDirect},
	} {
		selected := tc.selected
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile, SelectedAudioIndex: &selected})
		if err != nil {
			t.Fatalf("Info(selected %d): %v", selected, err)
		}
		grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
		if err != nil {
			t.Fatalf("selected %d: grant: %v (stream %+v)", selected, err, resp.Streams[0])
		}
		if grant.AudioStreamIndex != selected+1 || (grant.HLSAudioCodec == "copy") != tc.wantCopy {
			t.Fatalf("selected %d: grant audio index %d codec %q, want index %d copy=%v", selected, grant.AudioStreamIndex, grant.HLSAudioCodec, selected+1, tc.wantCopy)
		}
		if q := resp.AudioQuality; q == nil || q.SampleRate != tc.wantRate || q.Delivery != tc.wantDelivery {
			t.Fatalf("selected %d: audioQuality = %+v, want %d Hz %s", selected, q, tc.wantRate, tc.wantDelivery)
		}
	}
}

// TestComposedPictureCopyingAudioReportsDirect: a live composed picture that
// copies the source audio reports delivery "direct", and the same picture
// under a tempo adjustment, which re-encodes the audio, reports "transcode"
// (CORE-1627).
func TestComposedPictureCopyingAudioReportsDirect(t *testing.T) {
	profile := videoClient
	profile.AudioCodecs = []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}}
	profile.DownstreamAudio = "passthrough"
	stream := store.MediaFileStream{Index: 0, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48000, BitDepth: 24, Default: true}
	for _, tc := range []struct {
		name         string
		adjustment   *PlaybackAdjustment
		wantDelivery string
	}{
		{"copied", nil, AudioDeliveryDirect},
		{"tempo adjusted", &PlaybackAdjustment{Tempo: 1.1}, AudioDeliveryTranscode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			mediaID := importAudioOnlyMedia(t, st, "fp-composed-copy", stream)
			service.SetPictureComposer(staticComposer(ComposedPicture{TimedText: composedTestText}, true, nil))
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile, Adjustment: tc.adjustment})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Reason != "composed-picture" || len(resp.Streams) != 1 || !strings.HasPrefix(resp.Streams[0].ID, "composed-") {
				t.Fatalf("reason = %q streams = %+v, want one live composed stream", resp.Reason, resp.Streams)
			}
			grant := hlsGrant(t, resp.Streams[0].Token)
			if !grant.ComposedPicture || grant.HLSAudioCodec != "copy" {
				t.Fatalf("grant = %+v, want a live composed grant copying audio", grant)
			}
			want := AudioQuality{Codec: "truehd", SampleRate: 48000, BitDepth: 24, Lossless: true, Delivery: tc.wantDelivery}
			if resp.AudioQuality == nil || *resp.AudioQuality != want {
				t.Fatalf("audioQuality = %+v, want %+v", resp.AudioQuality, want)
			}
		})
	}
}
