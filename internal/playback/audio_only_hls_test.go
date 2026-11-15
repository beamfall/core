package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// coverArtTranscodeClient cannot play the mp3 container, so an audio item is
// transcoded, and it declares a 2160p decode ceiling, the shape of the client
// CORE-1542 was found on.
var coverArtTranscodeClient = CapabilityProfile{
	Client:       "beamfall-test",
	Containers:   []string{"mp4"},
	DirectCodecs: []string{"h264", "aac"},
	VideoCodecs:  []VideoCodecCap{{Codec: "h264", MaxHeight: 2160}},
}

// TestAudioClassTranscodeIsAnAudioOnlyRendition covers CORE-1542: an audio
// item carrying an embedded picture stream (cover art, recorded as a video
// stream row) is transcoded as audio only. Neither the playback-info
// renditions nor the HLS master advertise a video rung, and the one
// rendition's BANDWIDTH is the audio allowance, not a video ladder ceiling.
func TestAudioClassTranscodeIsAnAudioOnlyRendition(t *testing.T) {
	for name, size := range map[string][2]int{
		"cover art dimensions recorded": {1000, 1000},
		"no dimensions recorded":        {0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			service, st, scope := liveComposedService(t)
			path := filepath.Join(t.TempDir(), "track.mp3")
			if err := os.WriteFile(path, []byte("beamfall audio bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
				LibraryID: "library-local", Path: path, Title: "Track", Container: "mp3", Codec: "mp3",
				Class: store.MediaClassAudio, ContentFingerprint: "fp-cover-art", DurationSecondsHint: 30,
				VideoWidthHint: size[0], VideoHeightHint: size[1],
				Streams: []store.MediaFileStream{
					{Index: 0, Type: "video", Codec: "mjpeg"},
					audioStream(1, "mp3", 2),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: coverArtTranscodeClient})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Decision != "transcode" || len(resp.Streams) != 1 || resp.Streams[0].Kind != "hls" {
				t.Fatalf("decision = %q streams = %+v, want one HLS transcode", resp.Decision, resp.Streams)
			}
			want := RenditionInfo{ID: audioOnlyRungID, Bandwidth: bandwidthFor(0)}
			if got := resp.Streams[0].Renditions; len(got) != 1 || got[0] != want {
				t.Fatalf("renditions = %+v, want only %+v", got, want)
			}

			grant := hlsGrant(t, resp.Streams[0].Token)
			key := SessionKey{SessionID: grant.SessionID, MediaID: grant.MediaID, ProfileCapability: grant.ProfileCapability}
			master, err := service.sessions.FilePathShaped(context.Background(), key, path, media.DurationSeconds, media.VideoWidth, media.VideoHeight,
				grant.AudioCopy, grant.Degraded, LadderCapsFromGrant(grant), VideoSignal{}, -1, HLSAudioProfileFromGrant(grant), nil,
				StreamShapeFromGrant(grant, media), PriorityNormal, masterPlaylistName)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Clean(master))
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if strings.Count(text, "#EXT-X-STREAM-INF") != 1 || strings.Contains(text, "RESOLUTION") ||
				!strings.Contains(text, "BANDWIDTH=256000") || !strings.Contains(text, audioOnlyRungID+"/index.m3u8") {
				t.Fatalf("master = %q, want one audio-only variant at the audio allowance", text)
			}
			if _, err := service.sessions.FilePathShaped(context.Background(), key, path, media.DurationSeconds, media.VideoWidth, media.VideoHeight,
				grant.AudioCopy, grant.Degraded, LadderCapsFromGrant(grant), VideoSignal{}, -1, HLSAudioProfileFromGrant(grant), nil,
				StreamShapeFromGrant(grant, media), PriorityNormal, audioOnlyRungID+"/seg00000.m4s"); err != nil {
				t.Fatalf("first segment: %v", err)
			}
			transcoder := service.sessions.transcoder.(*fakeTranscoder)
			if len(transcoder.renditions) != 1 || !transcoder.renditions[0].AudioOnly {
				t.Fatalf("encodes = %+v, want one audio-only encode", transcoder.renditions)
			}
		})
	}
}
