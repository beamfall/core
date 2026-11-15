package playback

import (
	"testing"

	"github.com/beamfall/core/internal/store"
)

// TestCoverArtDoesNotGateAudioDirectPlay pins CORE-1848: an audio-class
// file's embedded cover art (a picture stream row) is not video, so it never
// decides direct play. Each case direct-plays the same track with and without
// the art, and the refusal cause, when there is one, never names the picture.
func TestCoverArtDoesNotGateAudioDirectPlay(t *testing.T) {
	media := store.Media{Class: store.MediaClassAudio, Container: "mp3", Codec: "mp3", VideoWidth: 1000, VideoHeight: 1000, DurationSeconds: 200}
	file := store.MediaFile{SizeBytes: 8_000_000}
	bare := []store.MediaFileStream{audioStream(0, "mp3", 2)}
	covered := []store.MediaFileStream{{Index: 0, Type: "video", Codec: "mjpeg"}, audioStream(1, "mp3", 2)}
	audioEnvelope := []AudioCodecCap{{Codec: "mp3", MaxChannels: 2}}
	for _, tc := range []struct {
		name    string
		profile CapabilityProfile
		ov      quirkOverlay
	}{
		{"audio envelope without a video envelope", CapabilityProfile{Containers: []string{"mp3"}, DirectCodecs: []string{"aac"}, AudioCodecs: audioEnvelope}, quirkOverlay{}},
		{"video and audio envelopes", CapabilityProfile{Containers: []string{"mp3"}, VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 720}}, AudioCodecs: audioEnvelope}, quirkOverlay{}},
		{"legacy direct codecs", CapabilityProfile{Containers: []string{"mp3"}, DirectCodecs: []string{"mp3"}}, quirkOverlay{}},
		{"quirk height cap below the art", CapabilityProfile{Containers: []string{"mp3"}, AudioCodecs: audioEnvelope}, quirkOverlay{MaxHeight: 720}},
		{"quirk video codec deny", CapabilityProfile{Containers: []string{"mp3"}, AudioCodecs: audioEnvelope}, quirkOverlay{DenyVideoCodecs: []string{"hevc"}}},
		{"quirk hdr deny", CapabilityProfile{Containers: []string{"mp3"}, AudioCodecs: audioEnvelope}, quirkOverlay{DenyHDRFormats: []string{"hdr10"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for label, streams := range map[string][]store.MediaFileStream{"bare": bare, "cover art": covered} {
				if !canDirectPlay(media, file, streams, tc.profile, nil, tc.ov) {
					t.Fatalf("%s: canDirectPlay = false, cause %q", label, deviceRefusalCause(media, file, streams, tc.profile, nil, tc.ov))
				}
			}
		})
	}
	t.Run("an undeclared audio codec is an audio refusal, art or not", func(t *testing.T) {
		profile := CapabilityProfile{Containers: []string{"mp3"}, VideoCodecs: []VideoCodecCap{{Codec: "mjpeg"}}, AudioCodecs: []AudioCodecCap{{Codec: "aac"}}}
		for label, streams := range map[string][]store.MediaFileStream{"bare": bare, "cover art": covered} {
			if canDirectPlay(media, file, streams, profile, nil, quirkOverlay{}) {
				t.Fatalf("%s: canDirectPlay = true for an undeclared audio codec", label)
			}
			if got := deviceRefusalCause(media, file, streams, profile, nil, quirkOverlay{}); got != PlayReasonAudio {
				t.Fatalf("%s: cause = %q, want %q", label, got, PlayReasonAudio)
			}
		}
	})
}
