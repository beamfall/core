package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

// TestAudioOnlyBandwidthFollowsTheAudioProfile pins CORE-1849: the audio-only
// rung's BANDWIDTH is the peak of the audio it carries, not a fixed stereo-AAC
// allowance.
func TestAudioOnlyBandwidthFollowsTheAudioProfile(t *testing.T) {
	truehd := store.MediaFileStream{Index: 0, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48_000, BitDepth: 24}
	dtsMA := store.MediaFileStream{Index: 0, Type: "audio", Codec: "dts", Profile: "DTS-HD MA", Channels: 8, SampleRate: 192_000, BitDepth: 24}
	for _, tc := range []struct {
		name     string
		audio    ffmpeg.AudioProfile
		source   store.MediaFileStream
		adjusted bool
		want     int
	}{
		{"stereo aac keeps the ladder allowance", ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, store.MediaFileStream{}, false, bandwidthFor(0)},
		{"unset channels encode stereo", ffmpeg.AudioProfile{Codec: "aac"}, store.MediaFileStream{}, false, 256_000},
		{"eac3 5.1", ffmpeg.AudioProfile{Codec: "eac3", Channels: 6}, store.MediaFileStream{}, false, 6*96_000 + 128_000},
		{"truehd passthrough at its PCM rate", ffmpeg.AudioProfile{Codec: "copy"}, truehd, false, 8*48_000*24 + 128_000},
		{"dts-hd ma passthrough at the codec peak", ffmpeg.AudioProfile{Codec: "copy"}, dtsMA, false, 24_500_000 + 128_000},
		{"truehd passthrough with no recorded channels", ffmpeg.AudioProfile{Codec: "copy"}, store.MediaFileStream{Type: "audio", Codec: "truehd"}, false, 18_000_000 + 128_000},
		{"passthrough with unknown rate and depth", ffmpeg.AudioProfile{Codec: "copy"}, store.MediaFileStream{Type: "audio", Codec: "truehd", Channels: 2}, false, 2*192_000*24 + 128_000},
		{"passthrough with no source stream takes the worst case", ffmpeg.AudioProfile{Codec: "copy"}, store.MediaFileStream{}, false, 24_500_000 + 128_000},
		{"adjusted passthrough is re-encoded as stereo aac", ffmpeg.AudioProfile{Codec: "copy"}, truehd, true, 256_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := audioOnlyBandwidth(tc.audio, tc.source, tc.adjusted); got != tc.want {
				t.Fatalf("audioOnlyBandwidth = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAudioOnlyRungFallsBackToTheLadderAllowance(t *testing.T) {
	if got := audioOnlyRung(0).Bandwidth; got != bandwidthFor(0) {
		t.Fatalf("audioOnlyRung(0).Bandwidth = %d, want %d", got, bandwidthFor(0))
	}
	if got := audioOnlyRung(704_000).Bandwidth; got != 704_000 {
		t.Fatalf("audioOnlyRung(704000).Bandwidth = %d", got)
	}
}

func TestAudioSourceStreamPicksTheMappedTrack(t *testing.T) {
	streams := []store.MediaFileStream{{Index: 0, Type: "video"}, audioStream(1, "aac", 2), audioStream(2, "truehd", 8)}
	if got := AudioSourceStream(streams, nil); got.Index != 1 {
		t.Fatalf("no selection maps stream %d, want the first audio stream 1", got.Index)
	}
	selected := 2
	if got := AudioSourceStream(streams, &selected); got.Index != 2 {
		t.Fatalf("selection 2 maps stream %d", got.Index)
	}
	missing := 9
	if got := AudioSourceStream(streams, &missing); got.Type != "" {
		t.Fatalf("missing selection maps %+v, want none", got)
	}
}

// TestAudioOnlyRenditionAdvertisesItsAudioPeak covers CORE-1849 end to end:
// the playback-info rendition and the HLS master the byte layer writes both
// advertise the audio-only peak for stereo, E-AC-3 5.1 and passthrough audio.
func TestAudioOnlyRenditionAdvertisesItsAudioPeak(t *testing.T) {
	for _, tc := range []struct {
		name   string
		client CapabilityProfile
		stream store.MediaFileStream
		want   int
	}{
		{"stereo aac", coverArtTranscodeClient, audioStream(0, "flac", 2), 256_000},
		{"eac3 5.1", CapabilityProfile{
			Client: "beamfall-test", Containers: []string{"mp4"}, DirectCodecs: []string{"aac"},
			AudioCodecs: []AudioCodecCap{{Codec: "eac3", MaxChannels: 6}},
		}, audioStream(0, "flac", 6), 704_000},
		{"truehd passthrough", CapabilityProfile{
			Client: "beamfall-test", Containers: []string{"mp4"}, DirectCodecs: []string{"aac"}, DownstreamAudio: "passthrough",
			AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}},
		}, store.MediaFileStream{Index: 0, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48_000, BitDepth: 24}, 9_344_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			path := filepath.Join(t.TempDir(), "track.mka")
			if err := os.WriteFile(path, []byte("beamfall audio bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			streams := []store.MediaFileStream{tc.stream}
			media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: "Track", Container: "matroska", Codec: tc.stream.Codec,
				Class: store.MediaClassAudio, ContentFingerprint: "fp-audio-peak", DurationSecondsHint: 30, Streams: streams,
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: tc.client})
			if err != nil {
				t.Fatal(err)
			}
			want := RenditionInfo{ID: audioOnlyRungID, Bandwidth: tc.want}
			if len(resp.Streams) != 1 || resp.Streams[0].Kind != "hls" || len(resp.Streams[0].Renditions) != 1 || resp.Streams[0].Renditions[0] != want {
				t.Fatalf("decision = %q streams = %+v, want one HLS rendition %+v", resp.Decision, resp.Streams, want)
			}
			grant := hlsGrant(t, resp.Streams[0].Token)
			key := SessionKey{SessionID: grant.SessionID, MediaID: grant.MediaID, ProfileCapability: grant.ProfileCapability}
			shape := StreamShapeFromGrant(grant, media)
			shape.AudioSource = AudioSourceStream(streams, nil)
			master, err := service.sessions.FilePathShaped(context.Background(), key, path, media.DurationSeconds, media.VideoWidth, media.VideoHeight,
				grant.AudioCopy, grant.Degraded, LadderCapsFromGrant(grant), VideoSignal{}, -1, HLSAudioProfileFromGrant(grant), nil,
				shape, PriorityNormal, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Clean(master))
			if err != nil {
				t.Fatal(err)
			}
			if text := string(body); strings.Count(text, "#EXT-X-STREAM-INF") != 1 || !strings.Contains(text, fmt.Sprintf("BANDWIDTH=%d,", tc.want)) && !strings.Contains(text, fmt.Sprintf("BANDWIDTH=%d\n", tc.want)) {
				t.Fatalf("master = %q, want BANDWIDTH=%d", text, tc.want)
			}
		})
	}
}
