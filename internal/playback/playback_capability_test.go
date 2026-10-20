package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

// importProbedMedia seeds one Media item with a probed per-stream inventory
// (PLAY-1 shape) and returns its derived MediaID. The richer CapabilityProfile
// reasons over these streams, so the test must persist real codec profile /
// HDR / channel data, not just the Media row's container+codec.
func importProbedMedia(t *testing.T, st *store.Store, fingerprint, container, codec string, width, height int, streams []store.MediaFileStream) store.MediaID {
	return importProbedMediaSized(t, st, fingerprint, container, codec, width, height, 0, streams)
}

func importProbedMediaSized(t *testing.T, st *store.Store, fingerprint, container, codec string, width, height int, sizeBytes int64, streams []store.MediaFileStream) store.MediaID {
	t.Helper()
	path := filepath.Join(t.TempDir(), fingerprint+".bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:           "library-local",
		Path:                path,
		Title:               "Probed " + fingerprint,
		Container:           container,
		Codec:               codec,
		Class:               store.MediaClassVideo,
		ContentFingerprint:  fingerprint,
		DurationSecondsHint: 120,
		VideoWidthHint:      width,
		VideoHeightHint:     height,
		SizeBytes:           sizeBytes,
		Streams:             streams,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return media.ID
}

func capabilityTestService(t *testing.T) (*Service, *store.Store, auth.Scope) {
	t.Helper()
	st := storetest.Open(t)
	scope, err := auth.NewService(st).Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	now := time.Unix(100, 0).UTC()
	clock := func() time.Time { return now }
	tokens := streamtoken.NewService([]byte("test-token-key"), clock)
	svc := NewService(st, tokens, clock)
	// Wire the opaque selector so Parts carry resolvable selectors (ADR-0098
	// area 2); tests resolve them back to Version/Part coordinates.
	svc.SetSelector(NewSelectorService([]byte("test-token-key"), clock))
	return svc, st, scope
}

// videoStream / audioStream build minimal inventory rows for the table below.
func videoStream(codec, profile string, doviProfile int, colorTransfer string) store.MediaFileStream {
	return store.MediaFileStream{Index: 0, Type: "video", Codec: codec, Profile: profile, DoViProfile: doviProfile, ColorTransfer: colorTransfer}
}

func videoStreamLevel(codec, profile string, level int) store.MediaFileStream {
	return store.MediaFileStream{Index: 0, Type: "video", Codec: codec, Profile: profile, Level: level}
}

func hdr10PlusVideoStream(codec, profile string) store.MediaFileStream {
	return store.MediaFileStream{Index: 0, Type: "video", Codec: codec, Profile: profile, ColorTransfer: "smpte2084", HDR10Plus: true}
}

func audioStream(index int, codec string, channels int) store.MediaFileStream {
	return store.MediaFileStream{Index: index, Type: "audio", Codec: codec, Channels: channels}
}

// main10_1080p is the acceptance profile: HEVC Main 10 up to 1080p, no Dolby
// Vision (HDR10/HLG only), no TrueHD (E-AC-3/AAC up to 6 channels).
var main10_1080p = CapabilityProfile{
	Client:     "beamfall-test",
	Containers: []string{"mkv", "mp4"},
	VideoCodecs: []VideoCodecCap{
		{Codec: "hevc", Profiles: []string{"Main", "Main 10"}, MaxHeight: 1080},
		{Codec: "h264", Profiles: []string{"High", "Main"}, MaxHeight: 1080},
	},
	AudioCodecs: []AudioCodecCap{
		{Codec: "eac3", MaxChannels: 6},
		{Codec: "aac", MaxChannels: 6},
	},
	HDRFormats: []string{"hdr10", "hlg"},
}

func TestCanDirectPlayRichProfile(t *testing.T) {
	tests := []struct {
		name      string
		container string
		codec     string
		height    int
		streams   []store.MediaFileStream
		profile   CapabilityProfile
		want      string // direct-play | transcode
	}{
		{
			// The headline acceptance case: a 4K Dolby Vision TrueHD file routed
			// against the Main10-1080p profile must transcode, not false
			// direct-play. Two ceilings independently force it (resolution and
			// HDR), and TrueHD audio is a third — any one is sufficient.
			name:      "4K Dolby Vision TrueHD against Main10/1080p transcodes",
			container: "mkv",
			codec:     "hevc",
			height:    2160,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 8, "smpte2084"),
				audioStream(1, "truehd", 8),
			},
			profile: main10_1080p,
			want:    "transcode",
		},
		{
			name:      "HEVC Main10 1080p HDR10 EAC3 direct-plays",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, "smpte2084"),
				audioStream(1, "eac3", 6),
			},
			profile: main10_1080p,
			want:    "direct-play",
		},
		{
			name:      "resolution over ceiling alone transcodes",
			container: "mkv",
			codec:     "hevc",
			height:    2160,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, "smpte2084"),
				audioStream(1, "eac3", 6),
			},
			profile: main10_1080p,
			want:    "transcode",
		},
		{
			name:      "Dolby Vision alone (1080p) transcodes when DV unsupported",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 8, "smpte2084"),
				audioStream(1, "eac3", 6),
			},
			profile: main10_1080p,
			want:    "transcode",
		},
		{
			name:      "HDR10+ transcodes when only HDR10 is supported",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				hdr10PlusVideoStream("hevc", "Main 10"),
				audioStream(1, "eac3", 6),
			},
			profile: main10_1080p,
			want:    "transcode",
		},
		{
			name:      "HDR10+ direct-plays when declared",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				hdr10PlusVideoStream("hevc", "Main 10"),
				audioStream(1, "eac3", 6),
			},
			profile: CapabilityProfile{
				Client:      main10_1080p.Client,
				Containers:  main10_1080p.Containers,
				VideoCodecs: main10_1080p.VideoCodecs,
				AudioCodecs: main10_1080p.AudioCodecs,
				HDRFormats:  []string{"hdr10", "hdr10plus", "hlg"},
			},
			want: "direct-play",
		},
		{
			// Video direct-playable, audio not: copy the video, transcode
			// only the audio — not a full re-encode.
			name:      "TrueHD audio alone with playable video copies video",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "truehd", 8),
			},
			profile: main10_1080p,
			want:    "direct-stream",
		},
		{
			// Audio over the channel ceiling with playable video — same audio-copy
			// path.
			name:      "audio channels over ceiling with playable video copies video",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "eac3", 8),
			},
			profile: main10_1080p,
			want:    "direct-stream",
		},
		{
			name:      "codec profile not in envelope transcodes",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 12", 0, ""),
				audioStream(1, "eac3", 6),
			},
			profile: main10_1080p,
			want:    "transcode",
		},
		{
			name:      "SDR Main10 1080p with multiple audio streams direct-plays",
			container: "mkv",
			codec:     "hevc",
			height:    1080,
			streams: []store.MediaFileStream{
				videoStream("hevc", "Main 10", 0, ""),
				audioStream(1, "eac3", 6),
				audioStream(2, "aac", 2),
			},
			profile: main10_1080p,
			want:    "direct-play",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			// Distinct fingerprint per case keeps the imported Media isolated.
			// Index-based so the value is path-safe (case names carry slashes).
			fingerprint := fmt.Sprintf("fp-case-%d", i)
			mediaID := importProbedMedia(t, st, fingerprint, tc.container, tc.codec, tc.height*16/9, tc.height, tc.streams)

			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: tc.profile,
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

func TestCapabilityProfileTierUsesEnvelopeUnderStableClientIdentity(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-tiered-profile", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})

	low := main10_1080p
	low.Client = "beamfall-roku"
	low.Tier = "legacy"
	low.VideoCodecs = []VideoCodecCap{{Codec: "h264", Profiles: []string{"High"}, MaxHeight: 1080}}
	high := main10_1080p
	high.Client = low.Client
	high.Tier = "current"

	for _, tc := range []struct {
		name    string
		profile CapabilityProfile
		want    string
	}{
		{name: "low tier transcodes from its envelope", profile: low, want: "transcode"},
		{name: "high tier direct-plays from its envelope", profile: high, want: "direct-play"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: tc.profile,
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

// TestCapabilityProfileIdentityStringsDoNotDecide is the converse of the test
// above and closes the other half of the ADR-0156 §4 proof: holding the
// declared envelope fixed and varying only the identity strings
// (Client/Tier) must not move the verdict. Together the two tests pin the
// decision to the envelope alone — a policy branch keyed on a client or tier
// name would red exactly one of them. (Quirk overlays are the one sanctioned
// tier-keyed mechanism; none is registered on this Service, so the direct-play
// path here is envelope-only.)
func TestCapabilityProfileIdentityStringsDoNotDecide(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-identity-invariant", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})

	// One envelope, many identities — including names a client-name policy
	// branch would plausibly key on.
	identities := []struct{ client, tier string }{
		{client: "beamfall-test", tier: ""},
		{client: "beamfall-roku", tier: "legacy"},
		{client: "beamfall-roku", tier: "current"},
		{client: "beamfall-tv", tier: "legacy"},
		{client: "unknown-client", tier: "unknown-tier"},
	}

	const want = "direct-play"
	for _, id := range identities {
		t.Run(id.client+"/"+id.tier, func(t *testing.T) {
			profile := main10_1080p
			profile.Client = id.client
			profile.Tier = id.tier

			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: profile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if resp.Decision != want {
				t.Fatalf("Decision = %q, want %q — identity strings must not move the verdict for a fixed envelope", resp.Decision, want)
			}
		})
	}
}

func TestCanDirectPlayUsesKnownBitrateCeiling(t *testing.T) {
	// 90 MB over the 120 second fixture duration is a 6,000,000 bit/s source.
	media := store.Media{
		Container:       "mkv",
		Codec:           "hevc",
		DurationSeconds: 120,
		VideoWidth:      1920,
		VideoHeight:     1080,
	}
	file := store.MediaFile{SizeBytes: 90_000_000}
	streams := []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	}
	profile := main10_1080p
	profile.VideoCodecs = []VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 1080, MaxBitrate: 5_000_000}}

	if canDirectPlay(media, file, streams, profile, nil, quirkOverlay{}) {
		t.Fatal("canDirectPlay = true, want false (known bitrate exceeds cap)")
	}
}

func TestCanDirectPlayUsesCodecLevelCeiling(t *testing.T) {
	media := store.Media{Container: "mkv", Codec: "hevc", VideoHeight: 1080}
	streams := []store.MediaFileStream{
		videoStreamLevel("hevc", "Main 10", 153),
		audioStream(1, "eac3", 6),
	}
	profile := main10_1080p
	profile.VideoCodecs = []VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 1080, MaxLevel: 120}}

	if canDirectPlay(media, store.MediaFile{}, streams, profile, nil, quirkOverlay{}) {
		t.Fatal("canDirectPlay = true, want false (source codec level exceeds cap)")
	}
}

func TestCanDirectPlayPreservesUnknownHDRCapability(t *testing.T) {
	media := store.Media{Container: "mkv", Codec: "hevc", VideoHeight: 1080}
	streams := []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, "smpte2084"),
		audioStream(1, "eac3", 6),
	}
	profile := main10_1080p
	profile.HDRFormats = nil

	if !canDirectPlay(media, store.MediaFile{}, streams, profile, nil, quirkOverlay{}) {
		t.Fatal("canDirectPlay = false, want true when profile omits HDRFormats")
	}
}

func TestSubtitleFormatsCompatibility(t *testing.T) {
	selectedPGS := 1
	streams := []store.MediaFileStream{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"},
		{Index: 1, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Forced: true},
	}
	if !subtitleFormatsCompatible(streams, CapabilityProfile{}, nil, quirkOverlay{}) {
		t.Fatal("unselected subtitles must preserve legacy compatibility")
	}
	if subtitleFormatsCompatible(streams, CapabilityProfile{SubtitleFormats: []string{"srt", "webvtt"}}, &selectedPGS, quirkOverlay{}) {
		t.Fatal("PGS stream reported compatible with text-only subtitle profile")
	}
	if !subtitleFormatsCompatible(streams, CapabilityProfile{SubtitleFormats: []string{"pgs"}}, &selectedPGS, quirkOverlay{}) {
		t.Fatal("PGS stream reported incompatible with PGS-capable profile")
	}
}

func TestCanDirectPlayUsesSubtitleFormats(t *testing.T) {
	selectedPGS := 2
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-subtitle-gate", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
		{Index: selectedPGS, Type: "subtitle", Codec: "hdmv_pgs_subtitle", Forced: true},
	})
	profile := main10_1080p
	profile.HDRFormats = nil
	profile.SubtitleFormats = []string{"srt", "webvtt"}

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play when the unsupported PGS track is unselected", resp.Decision)
	}

	resp, err = service.Info(context.Background(), scope, InfoRequest{
		MediaID:               string(mediaID),
		CapabilityProfile:     profile,
		SelectedSubtitleIndex: &selectedPGS,
	})
	if err != nil {
		t.Fatalf("Info with selected PGS: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode for selected unsupported PGS subtitle", resp.Decision)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("HLS token: %v", err)
	}
	if got := grant.BurnSubtitleIndex - 1; got != selectedPGS {
		t.Fatalf("BurnSubtitleIndex = %d, want %d", got, selectedPGS)
	}
}

func TestCanDirectPlayUsesDownstreamAudioHint(t *testing.T) {
	streams := []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	}
	profile := main10_1080p
	profile.DownstreamAudio = "stereo"

	if canDirectPlay(store.Media{Container: "mkv", Codec: "hevc", VideoHeight: 1080}, store.MediaFile{}, streams, profile, nil, quirkOverlay{}) {
		t.Fatal("canDirectPlay = true, want false for 6-channel audio into stereo downstream")
	}
}

func TestHLSAudioProfileTargetsEAC3WhenDeclared(t *testing.T) {
	audio := hlsAudioProfile(CapabilityProfile{
		AudioCodecs:     []AudioCodecCap{{Codec: "aac", MaxChannels: 2}, {Codec: "eac3", MaxChannels: 6}},
		DownstreamAudio: "passthrough",
	}, nil, LoudnessData{}, false, quirkOverlay{})
	if audio.Codec != "eac3" || audio.Channels != 6 {
		t.Fatalf("audio profile = %+v, want eac3 5.1", audio)
	}
}

func TestHLSAudioProfileUsesAACStereoFloorForStereoSink(t *testing.T) {
	audio := hlsAudioProfile(CapabilityProfile{
		AudioCodecs:     []AudioCodecCap{{Codec: "eac3", MaxChannels: 6}},
		DownstreamAudio: "stereo",
	}, nil, LoudnessData{}, false, quirkOverlay{})
	if audio.Codec != "aac" || audio.Channels != 2 {
		t.Fatalf("audio profile = %+v, want aac stereo floor", audio)
	}
}

func TestHLSAudioGrantCarriesGainOnlyWithMeasurement(t *testing.T) {
	measured := hlsAudioProfile(CapabilityProfile{}, nil, LoudnessData{GainDB: -3.5}, true, quirkOverlay{})
	grant := withHLSAudioGrant(streamtoken.Grant{}, measured)
	if grant.HLSAudioGainDB == nil || *grant.HLSAudioGainDB != -3.5 {
		t.Fatalf("gain grant = %v, want -3.5", grant.HLSAudioGainDB)
	}

	unmeasured := hlsAudioProfile(CapabilityProfile{}, nil, LoudnessData{GainDB: -3.5}, false, quirkOverlay{})
	grant = withHLSAudioGrant(streamtoken.Grant{}, unmeasured)
	if grant.HLSAudioGainDB != nil {
		t.Fatalf("unmeasured gain grant = %v, want nil", grant.HLSAudioGainDB)
	}
}

func TestHLSAudioProfileUsesPassthroughOnlyForAVRHint(t *testing.T) {
	streams := []store.MediaFileStream{audioStream(1, "truehd", 8)}
	profile := CapabilityProfile{
		AudioCodecs:     []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}},
		DownstreamAudio: "passthrough",
	}
	if got := hlsAudioProfile(profile, streams, LoudnessData{}, false, quirkOverlay{}); got.Codec != "copy" {
		t.Fatalf("passthrough audio profile = %+v, want bitstream copy", got)
	}

	profile.DownstreamAudio = "stereo"
	if got := hlsAudioProfile(profile, streams, LoudnessData{}, false, quirkOverlay{}); got.Codec == "copy" {
		t.Fatalf("stereo audio profile = %+v, want PCM/downmix branch", got)
	}
}

func TestDTSHDMAPassthroughRequiresMasterAudioProfile(t *testing.T) {
	profile := CapabilityProfile{
		AudioCodecs:     []AudioCodecCap{{Codec: "dts-hd-ma", MaxChannels: 8}},
		DownstreamAudio: "passthrough",
	}
	ma := []store.MediaFileStream{audioStream(1, "dts", 8)}
	ma[0].Profile = "DTS-HD MA"
	if !audioDirectPlayable(ma, profile) {
		t.Fatal("DTS-HD MA stream was not accepted for declared passthrough profile")
	}

	core := []store.MediaFileStream{audioStream(1, "dts", 6)}
	core[0].Profile = "DTS"
	if audioDirectPlayable(core, profile) {
		t.Fatal("plain DTS matched dts-hd-ma passthrough profile")
	}

	dtsOnly := profile
	dtsOnly.AudioCodecs = []AudioCodecCap{{Codec: "dts", MaxChannels: 6}}
	if audioDirectPlayable(ma, dtsOnly) {
		t.Fatal("DTS-HD MA matched plain DTS profile")
	}
}

// TestAudioOnlyTranscodeDecision is the PLAY-3 acceptance case: a
// direct-playable video (HEVC Main 10, 1080p, SDR) with audio the profile
// cannot decode (TrueHD) resolves to direct-stream mode carrying an HLS choice
// whose single rendition is the audio-copy rung — not a full video re-encode.
func TestAudioOnlyTranscodeDecision(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-audio-copy", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "truehd", 8),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: main10_1080p,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-stream" {
		t.Fatalf("Decision = %q, want direct-stream (video direct-playable, audio not)", resp.Decision)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("Streams = %d, want exactly the audio-copy choice", len(resp.Streams))
	}
	choice := resp.Streams[0]
	if choice.Kind != "hls" {
		t.Fatalf("choice.Kind = %q, want hls", choice.Kind)
	}
	if len(choice.Renditions) != 1 || choice.Renditions[0].ID != audioCopyRungID {
		t.Fatalf("renditions = %v, want one %q rung", choice.Renditions, audioCopyRungID)
	}
	// The audio-copy rung must drive an -c:v copy ffmpeg rendition — the rung
	// the decision selects is the one the session builds its Rendition from.
	rung := audioCopyRung()
	if !rung.CopyVideo {
		t.Fatal("audioCopyRung must set CopyVideo so the ffmpeg rendition copies the video")
	}
}

// TestTranscodeLadderClippedToClientCeiling is the production wire-up for the
// capability-honest ladder: a 1080p source that must transcode
// for a client whose declared decode ceiling is 480p gets a ladder clipped to
// 480p — the advertised renditions never exceed what the client can play, not
// the full source ladder. The container mismatch forces the transcode verdict,
// so the HLS choice is the lone stream and its Renditions are the clipped
// ladder built at hlsChoice from the in-hand CapabilityProfile.
func TestTranscodeLadderClippedToClientCeiling(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	// A 1080p HEVC file in an mkv the client cannot direct-play (its containers
	// are mp4-only), forcing a full transcode.
	mediaID := importProbedMedia(t, st, "fp-clip-480", "mkv", "hevc", 1920, 1080, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "aac", 2),
	})
	// Client decodes only up to 480p (the tightest VideoCodecCap.MaxHeight).
	profile := CapabilityProfile{
		Client:     "low-ceiling-tv",
		Containers: []string{"mp4"},
		VideoCodecs: []VideoCodecCap{
			{Codec: "hevc", MaxHeight: 480},
			{Codec: "h264", MaxHeight: 480},
		},
		AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 6}},
	}

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode (container mismatch forces it)", resp.Decision)
	}
	if len(resp.Streams) != 1 {
		t.Fatalf("Streams = %d, want exactly the HLS transcode choice", len(resp.Streams))
	}
	rungs := resp.Streams[0].Renditions
	if len(rungs) != 1 || rungs[0].Height != 480 {
		t.Fatalf("renditions = %v, want a single 480p rung clipped to the client ceiling (not the full 1080p source ladder)", rungs)
	}
}

type hevcCapableTranscoder struct {
	fakeTranscoder
}

func (*hevcCapableTranscoder) SupportsTranscodeCodec(codec string) bool {
	return codec == "hevc"
}

func TestTranscodeLadderAddsHEVC4KRungWithClientCapAndHardware(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	sessions := NewSessions(&hevcCapableTranscoder{}, t.TempDir(), nil)
	defer sessions.Close()
	service.SetSessions(sessions)

	mediaID := importProbedMedia(t, st, "fp-clip-4k-hevc", "mkv", "h264", 3840, 2160, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
		audioStream(1, "aac", 2),
	})
	profile := CapabilityProfile{
		Client:     "hevc-4k-tv",
		Containers: []string{"mp4"},
		VideoCodecs: []VideoCodecCap{
			{Codec: "hevc", MaxHeight: 2160},
			{Codec: "h264", MaxHeight: 1080},
		},
		AudioCodecs: []AudioCodecCap{{Codec: "aac", MaxChannels: 6}},
	}

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "transcode" {
		t.Fatalf("Decision = %q, want transcode", resp.Decision)
	}
	rungs := resp.Streams[0].Renditions
	if len(rungs) == 0 || rungs[0].ID != "2160p-hevc" || rungs[0].Height != 2160 || rungs[0].Bandwidth != 25_256_000 {
		t.Fatalf("renditions = %v, want leading 2160p HEVC rung", rungs)
	}
	grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	if !grant.HLSHEVC || grant.HLSHEVCMaxHeight != 2160 || grant.HLSClientMaxHeight != 1080 {
		t.Fatalf("grant ladder caps = hevc:%v hevcHeight:%d h264Height:%d", grant.HLSHEVC, grant.HLSHEVCMaxHeight, grant.HLSClientMaxHeight)
	}
}

// TestCanDirectPlayLegacyProfileUnchanged guards the floor: a profile that
// declares only {Containers, DirectCodecs} (the pre-PLAY-2 wire shape) is
// judged exactly as before, ignoring the stream inventory.
func TestCanDirectPlayLegacyProfileUnchanged(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	// A 4K DV TrueHD file — would transcode under a rich profile, but the legacy
	// profile only checks container+codec membership.
	mediaID := importProbedMedia(t, st, "fp-legacy", "mp4", "h264", 3840, 2160, []store.MediaFileStream{
		videoStream("h264", "High", 8, "smpte2084"),
		audioStream(1, "truehd", 8),
	})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: CapabilityProfile{Containers: []string{"mp4"}, DirectCodecs: []string{"h264"}},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play (legacy profile is unchanged)", resp.Decision)
	}
}
