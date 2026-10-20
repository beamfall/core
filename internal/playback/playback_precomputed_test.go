package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestInfoSelectsCompatiblePrecomputedRenditionBeforeLiveTranscode(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-4k", "mkv", "hevc", 3840, 2160, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 8, "smpte2084"),
	})
	insertReadyPlaybackRendition(t, st, mediaID, "precompute-720p", 720)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxHeight: 720},
			},
			HDRFormats: []string{"hdr10"},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "precomputed-rendition" {
		t.Fatalf("Decision = %q, want precomputed-rendition", resp.Decision)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("Streams = %#v, want one cached HLS stream", resp.Streams)
	}
	stream := resp.Streams[0]
	if stream.Kind != "hls" || !strings.HasPrefix(stream.URL, "/api/media/hls/") || !strings.HasSuffix(stream.URL, "/master.m3u8") {
		t.Fatalf("stream = %#v, want HLS master URL", stream)
	}
	if len(stream.Renditions) != 1 || stream.Renditions[0].ID != "720p" || stream.Renditions[0].Height != 720 {
		t.Fatalf("Renditions = %#v, want cached 720p rung only", stream.Renditions)
	}

	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.UnverifiedGrant(stream.Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("cached stream token did not verify as HLS: %v", err)
	}
	if grant.PlaybackRenditionID != "precompute-720p" {
		t.Fatalf("PlaybackRenditionID = %q, want precompute-720p", grant.PlaybackRenditionID)
	}
}

func TestPrecomputedRenditionRequiresToneMapMarkerForHDRSource(t *testing.T) {
	dir := t.TempDir()
	rendition := store.PlaybackRendition{
		Kind:   store.PlaybackRenditionKindHLS,
		Status: store.PlaybackRenditionReady,
		Codec:  "h264",
		Height: 720,
		Path:   dir,
	}
	profile := CapabilityProfile{VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 720}}}
	sourceVideo := VideoSignal{ColorTransfer: "smpte2084", ColorPrimaries: "bt2020"}

	if precomputedRenditionCompatible(rendition, profile, sourceVideo, false, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, quirkOverlay{}) {
		t.Fatal("HDR-source precompute without tone-map marker must not be served")
	}
	if err := os.WriteFile(filepath.Join(dir, toneMappedSDRMarker), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !precomputedRenditionCompatible(rendition, profile, sourceVideo, false, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, quirkOverlay{}) {
		t.Fatal("HDR-source precompute with tone-map marker should be compatible")
	}
}

func TestPrecomputedRenditionAudioCompatibility(t *testing.T) {
	rendition := store.PlaybackRendition{Kind: store.PlaybackRenditionKindHLS, Status: store.PlaybackRenditionReady, Codec: "h264", Height: 720}
	profile := CapabilityProfile{VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 720}}}

	if precomputedRenditionCompatible(rendition, profile, VideoSignal{}, true, ffmpeg.AudioProfile{Codec: "eac3", Channels: 6}, quirkOverlay{}) {
		t.Fatal("AAC stereo precompute was accepted for an E-AC-3 5.1 transcode profile")
	}
	if !precomputedRenditionCompatible(rendition, profile, VideoSignal{}, true, ffmpeg.AudioProfile{Codec: "aac", Channels: 2}, quirkOverlay{}) {
		t.Fatal("AAC stereo precompute was rejected for an AAC stereo transcode profile")
	}
	if !precomputedRenditionCompatible(rendition, profile, VideoSignal{}, false, ffmpeg.AudioProfile{Codec: "eac3", Channels: 6}, quirkOverlay{}) {
		t.Fatal("video-only precompute was rejected for an irrelevant audio target")
	}
}

func TestInfoRejectsPrecomputedStereoAudioForEAC3Profile(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-eac3", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
		audioStream(1, "eac3", 6),
	})
	insertReadyPlaybackRendition(t, st, mediaID, "precompute-eac3-720p", 720)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:          "beamfall-test",
			Containers:      []string{"mp4"},
			VideoCodecs:     []VideoCodecCap{{Codec: "h264", MaxHeight: 720}},
			AudioCodecs:     []AudioCodecCap{{Codec: "aac", MaxChannels: 2}, {Codec: "eac3", MaxChannels: 6}},
			DownstreamAudio: "passthrough",
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want live transcode instead of cached AAC stereo", resp.Decision)
	}
}

func TestInfoRefusesDolbyVisionProfile5FullTranscode(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-dv-p5", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 5, ""),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxHeight: 1080},
			},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "unavailable" || resp.Reason != "dolby-vision-p5-transcode-unavailable" {
		t.Fatalf("Decision/Reason = %q/%q, want unavailable/dolby-vision-p5-transcode-unavailable", resp.Decision, resp.Reason)
	}
	if len(resp.Streams) != 0 {
		t.Fatalf("Streams = %#v, want none", resp.Streams)
	}
}

func TestInfoIgnoresIncompatiblePrecomputedRendition(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-incompatible", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	insertReadyPlaybackRendition(t, st, mediaID, "precompute-1080p", 1080)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxHeight: 720},
			},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want live transcode fallback", resp.Decision)
	}
	if len(resp.Streams) != 1 || len(resp.Streams[0].Renditions) == 0 || resp.Streams[0].Renditions[0].Height > 720 {
		t.Fatalf("Streams = %#v, want live transcode clipped to client ceiling", resp.Streams)
	}
}

func TestInfoIgnoresStalePrecomputedRenditionForOldFingerprint(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-current", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
	})
	insertReadyPlaybackRenditionWithFingerprint(t, st, mediaID, "precompute-stale-720p", "fp-precompute-old", 720)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxHeight: 720},
			},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want live transcode when cached fingerprint is stale", resp.Decision)
	}
	if len(resp.Streams) != 1 || len(resp.Streams[0].Renditions) == 0 {
		t.Fatalf("Streams = %#v, want live transcode fallback", resp.Streams)
	}
}

func TestInfoKeepsDirectPlayBeforePrecomputedRendition(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-precompute-direct", "mp4", "h264", 1280, 720, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
	})
	insertReadyPlaybackRendition(t, st, mediaID, "precompute-direct-720p", 720)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(mediaID),
		CapabilityProfile: CapabilityProfile{
			Client:       "beamfall-test",
			Containers:   []string{"mp4"},
			DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", resp.Decision)
	}
	if len(resp.Streams) == 0 || resp.Streams[0].Kind != "direct-play" {
		t.Fatalf("Streams = %#v, want direct-play first", resp.Streams)
	}
}

func TestInfoIssuesRestrictedPrecomputedTokenForRestrictedMedia(t *testing.T) {
	service, scope := newTestServiceWithMediaFile(t, "session-revealed", "media-archive")
	insertReadyPlaybackRendition(t, service.store, "media-archive", "restricted-precompute-480p", 480)

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: "media-archive",
		CapabilityProfile: CapabilityProfile{
			Client:     "beamfall-test",
			Containers: []string{"mp4"},
			VideoCodecs: []VideoCodecCap{
				{Codec: "h264", MaxHeight: 480},
			},
		},
	})
	if err != nil {
		t.Fatalf("Info returned error: %v", err)
	}
	if resp.Decision != "precomputed-rendition" {
		t.Fatalf("Decision = %q, want precomputed-rendition", resp.Decision)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(100, 0).UTC() })
	grant, err := tokens.Verify(resp.Streams[0].Token, streamtoken.TargetMediaHLS, scope)
	if err != nil {
		t.Fatalf("Verify with revealed scope returned error: %v", err)
	}
	if !grant.Restricted {
		t.Fatal("precomputed token for restricted media is not marked Restricted")
	}
	relocked := scope
	relocked.Revealed = false
	if _, err := tokens.Verify(resp.Streams[0].Token, streamtoken.TargetMediaHLS, relocked); err == nil {
		t.Fatal("restricted precomputed token verified after relock")
	}
}

func insertReadyPlaybackRendition(t *testing.T, st *store.Store, mediaID store.MediaID, id string, height int) {
	t.Helper()
	files, err := st.MediaFilesForMedia(context.Background(), mediaID, store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("MediaFilesForMedia: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("media %q has no source files", mediaID)
	}
	insertReadyPlaybackRenditionWithFingerprint(t, st, mediaID, id, files[0].ContentFingerprint, height)
}

func insertReadyPlaybackRenditionWithFingerprint(t *testing.T, st *store.Store, mediaID store.MediaID, id string, fingerprint string, height int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, toneMappedSDRMarker), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertPlaybackRendition(context.Background(), store.PlaybackRendition{
		ID:                 store.PlaybackRenditionID(id),
		MediaID:            mediaID,
		ContentFingerprint: fingerprint,
		Kind:               store.PlaybackRenditionKindHLS,
		Status:             store.PlaybackRenditionReady,
		RungID:             rendID(height),
		Codec:              "h264",
		Height:             height,
		Bandwidth:          4_256_000,
		Path:               dir,
		SizeBytes:          1024,
		CreatedAt:          time.Unix(100, 0).UTC(),
		LastUsedAt:         time.Unix(100, 0).UTC(),
	}); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}
}

func rendID(height int) string {
	if height == 720 {
		return "720p"
	}
	if height == 1080 {
		return "1080p"
	}
	return "480p"
}

// insertReadyPlaybackRenditionCodec is insertReadyPlaybackRendition with the
// rendition's own codec under test control: the §4(d) veto judges the CACHED
// rendition's shape, never the source's, so the two must be able to differ.
func insertReadyPlaybackRenditionCodec(t *testing.T, st *store.Store, mediaID store.MediaID, id, codec string, height int) {
	t.Helper()
	insertReadyPlaybackRendition(t, st, mediaID, id, height)
	rendition, err := st.PlaybackRendition(context.Background(), store.PlaybackRenditionID(id), store.LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil {
		t.Fatalf("PlaybackRendition(%s): %v", id, err)
	}
	rendition.Codec = codec
	if err := st.UpsertPlaybackRendition(context.Background(), rendition); err != nil {
		t.Fatalf("UpsertPlaybackRendition: %v", err)
	}
}

// cachedRenditionProfile declares both codecs at a 720p ceiling, so a 4K source
// always falls past direct-play into the cached-rendition branch and either
// codec's cached rendition is compatible on the base path.
var cachedRenditionProfile = CapabilityProfile{
	Client:      "tv-x",
	Containers:  []string{"mp4"},
	VideoCodecs: []VideoCodecCap{{Codec: "h264", MaxHeight: 720}, {Codec: "hevc", MaxHeight: 720}},
}

// TestQuirkOverlayVetoesCachedRenditionByRenditionShape pins the wrong-object
// bug in BOTH directions: reusing the source veto here would reject a cached
// H.264 rendition for a source-HEVC deny, and let a cached HEVC rendition slip
// past that same deny on an H.264 source.
func TestQuirkOverlayVetoesCachedRenditionByRenditionShape(t *testing.T) {
	denyHEVC := QuirkEntry{
		ID:       "tv-x-no-hevc",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{DenyVideoCodecs: []string{"hevc"}},
	}
	forceTranscode := QuirkEntry{
		ID:       "tv-x-force-transcode",
		Note:     "fixture",
		Selector: QuirkSelector{Client: "tv-x"},
		Override: QuirkOverride{ForceTranscode: true},
	}
	cases := []struct {
		name           string
		sourceCodec    string
		renditionCodec string
		entry          QuirkEntry
		want           string
	}{
		{"cached HEVC rendition is vetoed", "h264", "hevc", denyHEVC, "transcode"},
		{"cached H.264 rendition survives a source-HEVC deny", "hevc", "h264", denyHEVC, "precomputed-rendition"},
		{"forceTranscode does not veto a cached rendition", "h264", "h264", forceTranscode, "precomputed-rendition"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, tc.entry)
			mediaID := importProbedMedia(t, st, "fp-rendition-veto", "mkv", tc.sourceCodec, 3840, 2160, []store.MediaFileStream{
				videoStream(tc.sourceCodec, "High", 0, ""),
			})
			insertReadyPlaybackRenditionCodec(t, st, mediaID, "cached-720p", tc.renditionCodec, 720)
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: cachedRenditionProfile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != tc.want {
				t.Fatalf("Decision = %q, want %q", resp.Decision, tc.want)
			}
		})
	}
}

// TestQuirkUnverifiableOverrideRejectsCachedRendition proves the cached branch
// fails CLOSED on an override the rendition row cannot be checked against: it
// runs before the live transcode, so serving unverified would ship exactly the
// shape a matched quirk forbids. Falling through costs a live transcode, which
// IS checkable.
func TestQuirkUnverifiableOverrideRejectsCachedRendition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override QuirkOverride
	}{
		{"HDR deny", QuirkOverride{DenyHDRFormats: []string{"hdr10"}}},
		{"audio deny", QuirkOverride{DenyAudioCodecs: []string{"truehd"}}},
		{"channel cap", QuirkOverride{MaxAudioChannels: 2}},
		{"no override", QuirkOverride{ForceRemux: true}},
	} {
		name, override := tc.name, tc.override
		t.Run(name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			installQuirks(t, service, QuirkEntry{
				ID:       "tv-x-unverifiable-rendition",
				Note:     "fixture",
				Selector: QuirkSelector{Client: "tv-x"},
				Override: override,
			})
			mediaID := importProbedMedia(t, st, "fp-rendition-unverifiable", "mkv", "h264", 3840, 2160, []store.MediaFileStream{
				videoStream("h264", "High", 0, ""),
			})
			// The helper always writes the tone-map marker, so the HDR case also
			// proves the marker is not the affirmative evidence this rule needs.
			insertReadyPlaybackRenditionCodec(t, st, mediaID, "cached-720p", "h264", 720)
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: cachedRenditionProfile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			want := "transcode"
			if name == "no override" {
				// ForceRemux is absorbing over SOURCE passthrough only, so the
				// cached rendition — neither the source nor its container — is
				// still served.
				want = "precomputed-rendition"
			}
			if resp.Decision != want {
				t.Fatalf("Decision = %q, want %q", resp.Decision, want)
			}
		})
	}
}
