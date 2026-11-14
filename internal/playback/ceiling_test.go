package playback

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

// ceilingSourceBytes is a 6,000,000 bit/s source over the 120 second
// fixture duration.
const ceilingSourceBytes = 90_000_000

func ceilingInfo(t *testing.T, profile CapabilityProfile, sizeBytes int64) (*Service, InfoResponse, store.SessionID, store.MediaID) {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMediaSized(t, st, "fp-ceiling", "mkv", "hevc", 1920, 1080, sizeBytes, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return service, resp, scope.SessionID, mediaID
}

func withCeiling(profile CapabilityProfile, ceiling int) CapabilityProfile {
	profile.MaxStreamingBitrate = ceiling
	return profile
}

// TestStreamingCeilingDecision covers CORE-1674: the viewer's ceiling reaches
// the decision and names itself as the reason only when it alone applies.
func TestStreamingCeilingDecision(t *testing.T) {
	t.Run("no ceiling direct plays", func(t *testing.T) {
		_, resp, _, _ := ceilingInfo(t, main10_1080p, ceilingSourceBytes)
		if resp.Decision != "direct-play" {
			t.Fatalf("Decision = %q, want direct-play", resp.Decision)
		}
	})
	// The store does not read media_file sizes back yet (CORE-0555), so at
	// this level every source bitrate is unknown and a ceiling vetoes direct
	// play by the evidence rule; TestStreamingCeilingVetoUsesKnownBitrate
	// covers a known bitrate.
	t.Run("ceiling transcodes under it", func(t *testing.T) {
		service, resp, sessionID, mediaID := ceilingInfo(t, withCeiling(main10_1080p, 5_000_000), ceilingSourceBytes)
		if resp.Decision != "transcode" || resp.Reason != bitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, bitrateCeilingReason)
		}
		if len(resp.AppliedQuirks) != 0 {
			t.Fatalf("AppliedQuirks = %v, want none: the ceiling is not a quirk", resp.AppliedQuirks)
		}
		renditions := resp.Streams[0].Renditions
		if len(renditions) == 0 {
			t.Fatal("no renditions advertised")
		}
		for _, rendition := range renditions {
			if rendition.Bandwidth > 5_000_000 || rendition.Height > 720 {
				t.Fatalf("rung %q at %dp/%d bit/s exceeds the 5 Mbit/s ceiling", rendition.ID, rendition.Height, rendition.Bandwidth)
			}
		}
		obs, ok := service.PlayObservation(sessionID, mediaID)
		if !ok || obs.Method != PlayMethodTranscode || obs.Reason != PlayReasonBitrate {
			t.Fatalf("observation = %+v (%v), want transcode/%s", obs, ok, PlayReasonBitrate)
		}
	})
	t.Run("unknown source bitrate is not handed over under a ceiling", func(t *testing.T) {
		_, resp, _, _ := ceilingInfo(t, withCeiling(main10_1080p, 40_000_000), 0)
		if resp.Decision != "transcode" || resp.Reason != bitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, bitrateCeilingReason)
		}
	})
	t.Run("ceiling below the ladder floor plays the lowest rung", func(t *testing.T) {
		_, resp, _, _ := ceilingInfo(t, withCeiling(main10_1080p, 500_000), ceilingSourceBytes)
		if resp.Decision != "transcode" || resp.Reason != bitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, bitrateCeilingReason)
		}
		for _, rendition := range resp.Streams[0].Renditions {
			if rendition.Height > 480 {
				t.Fatalf("rung %q at %dp, want the lowest rung only", rendition.ID, rendition.Height)
			}
		}
	})
	t.Run("another refusal keeps its own reason", func(t *testing.T) {
		h264Only := main10_1080p
		h264Only.VideoCodecs = []VideoCodecCap{{Codec: "h264", Profiles: []string{"High", "Main"}, MaxHeight: 1080}}
		_, resp, _, _ := ceilingInfo(t, withCeiling(h264Only, 5_000_000), ceilingSourceBytes)
		if resp.Decision != "transcode" || resp.Reason != "requires-transcode" {
			t.Fatalf("decision = %q/%q, want transcode/requires-transcode", resp.Decision, resp.Reason)
		}
	})
}

// TestStreamingCeilingKeepsHEVCRungsThatFit checks that each codec takes its
// own rung heights: a 40 Mbit/s ceiling keeps the 2160p HEVC rung (about
// 25 Mbit/s), while a 10 Mbit/s ceiling fits no HEVC rung.
func TestStreamingCeilingKeepsHEVCRungsThatFit(t *testing.T) {
	cases := []struct {
		name     string
		ceiling  int
		wantTop  string
		wantHEVC int
	}{
		{"generous ceiling keeps 2160p HEVC", 40_000_000, "2160p-hevc", 2160},
		{"mid ceiling keeps 1440p HEVC", 20_000_000, "1440p-hevc", 1440},
		{"tight ceiling leaves HEVC off", 10_000_000, "1080p", 1080},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			sessions := NewSessions(&hevcCapableTranscoder{}, t.TempDir(), nil)
			defer sessions.Close()
			service.SetSessions(sessions)
			mediaID := importProbedMedia(t, st, "fp-ceiling-4k", "mkv", "h264", 3840, 2160, []store.MediaFileStream{
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
				AudioCodecs:         []AudioCodecCap{{Codec: "aac", MaxChannels: 6}},
				MaxStreamingBitrate: tc.ceiling,
			}
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			rungs := resp.Streams[0].Renditions
			if len(rungs) == 0 || rungs[0].ID != tc.wantTop {
				t.Fatalf("renditions = %v, want %s on top", rungs, tc.wantTop)
			}
			for _, rung := range rungs {
				if rung.Bandwidth > tc.ceiling {
					t.Fatalf("rung %q at %d bit/s exceeds the %d bit/s ceiling", rung.ID, rung.Bandwidth, tc.ceiling)
				}
			}
			grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("grant: %v", err)
			}
			if grant.HLSHEVCMaxHeight != tc.wantHEVC || grant.HLSClientMaxHeight != 1080 {
				t.Fatalf("grant heights = hevc:%d h264:%d, want hevc:%d h264:1080", grant.HLSHEVCMaxHeight, grant.HLSClientMaxHeight, tc.wantHEVC)
			}
		})
	}
}

// TestStreamingCeilingDoesNotMaskOtherRefusals checks that the dashboard
// names the codec refusal, not the ceiling, when both apply.
func TestStreamingCeilingDoesNotMaskOtherRefusals(t *testing.T) {
	h264Only := main10_1080p
	h264Only.VideoCodecs = []VideoCodecCap{{Codec: "h264", Profiles: []string{"High", "Main"}, MaxHeight: 1080}}
	service, _, sessionID, mediaID := ceilingInfo(t, withCeiling(h264Only, 5_000_000), ceilingSourceBytes)
	obs, ok := service.PlayObservation(sessionID, mediaID)
	if !ok || obs.Reason != PlayReasonVideoCodec {
		t.Fatalf("observation = %+v (%v), want reason %s", obs, ok, PlayReasonVideoCodec)
	}
}

// TestStreamingCeilingLeavesCachedRenditionReason checks that a cached
// rendition the item would have taken anyway keeps its own reason: an mkv
// source on an mp4-only client never direct plays, ceiling or not.
func TestStreamingCeilingLeavesCachedRenditionReason(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	mediaID := importProbedMedia(t, st, "fp-ceiling-cached", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
		videoStream("h264", "High", 0, ""),
		audioStream(1, "aac", 2),
	})
	insertReadyPlaybackRendition(t, st, mediaID, "precompute-720p", 720)
	profile := CapabilityProfile{
		Client:              "beamfall-test",
		Containers:          []string{"mp4"},
		VideoCodecs:         []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
		AudioCodecs:         []AudioCodecCap{{Codec: "aac", MaxChannels: 2}},
		MaxStreamingBitrate: 40_000_000,
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "precomputed-rendition" || resp.Reason != "cached-rendition" {
		t.Fatalf("decision = %q/%q, want precomputed-rendition/cached-rendition", resp.Decision, resp.Reason)
	}
}

func TestStreamingCeilingVetoUsesKnownBitrate(t *testing.T) {
	media := store.Media{Class: store.MediaClassVideo, Container: "mkv", Codec: "hevc", DurationSeconds: 120, VideoHeight: 1080}
	file := store.MediaFile{SizeBytes: ceilingSourceBytes}
	streams := []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)}
	if !canDirectPlay(media, file, streams, main10_1080p, nil, quirkOverlay{}) {
		t.Fatal("fixture must direct play without a ceiling")
	}
	if !canDirectPlay(media, file, streams, main10_1080p, nil, quirkOverlay{}.withClientCeiling(10_000_000)) {
		t.Fatal("a 10 Mbit/s ceiling refused a 6 Mbit/s source")
	}
	under := quirkOverlay{}.withClientCeiling(5_000_000)
	if canDirectPlay(media, file, streams, main10_1080p, nil, under) {
		t.Fatal("a 5 Mbit/s ceiling direct played a 6 Mbit/s source")
	}
	if !bitrateCeilingForced(media, file, streams, main10_1080p, nil, under) {
		t.Fatal("bitrateCeilingForced = false, want true: the ceiling alone refused direct play")
	}
	if got := directPlayRefusalCause(media, file, streams, main10_1080p, nil, under); got != PlayReasonBitrate {
		t.Fatalf("refusal cause = %q, want %q", got, PlayReasonBitrate)
	}
	quirked := quirkOverlay{DenyVideoCodecs: []string{"hevc"}}.withClientCeiling(5_000_000)
	if bitrateCeilingForced(media, file, streams, main10_1080p, nil, quirked) {
		t.Fatal("bitrateCeilingForced = true while a quirk also refuses the source")
	}
	if got := directPlayRefusalCause(media, file, streams, main10_1080p, nil, quirked); got != PlayReasonDeviceQuirk {
		t.Fatalf("refusal cause = %q, want %q", got, PlayReasonDeviceQuirk)
	}
}

func TestWithClientCeilingBounds(t *testing.T) {
	if got := (quirkOverlay{}).withClientCeiling(0).maxBitrate(); got != 0 {
		t.Fatalf("zero ceiling maxBitrate = %d, want 0", got)
	}
	if got := (quirkOverlay{}).withClientCeiling(-1).maxBitrate(); got != 0 {
		t.Fatalf("negative ceiling maxBitrate = %d, want 0", got)
	}
	if got := (quirkOverlay{}).withClientCeiling(1).maxBitrate(); got != ladderFloorBandwidth() {
		t.Fatalf("sub-floor ceiling maxBitrate = %d, want the ladder floor %d", got, ladderFloorBandwidth())
	}
	quirk := quirkOverlay{MaxBitrate: 6_000_000}
	if got := quirk.withClientCeiling(20_000_000).maxBitrate(); got != 6_000_000 {
		t.Fatalf("maxBitrate = %d, want the tighter quirk cap", got)
	}
	if got := quirk.withClientCeiling(3_000_000).maxBitrate(); got != 3_000_000 {
		t.Fatalf("maxBitrate = %d, want the tighter viewer ceiling", got)
	}
	ceiled := quirkOverlay{matched: true, overrideIDs: []string{"q"}}.withClientCeiling(3_000_000)
	if base := ceiled.withoutClientCeiling(); base.clientMaxBitrate != 0 || !base.matched || len(base.overrideIDs) != 1 {
		t.Fatalf("withoutClientCeiling = %+v, want only the ceiling dropped", base)
	}
}

func TestStreamingCeilingExemptsAudioOnly(t *testing.T) {
	media := store.Media{Class: store.MediaClassAudio, Container: "flac", Codec: "flac", DurationSeconds: 120}
	streams := []store.MediaFileStream{audioStream(0, "flac", 2)}
	ov := quirkOverlay{}.withClientCeiling(2_000_000)
	if ov.vetoesVideoPassthrough(media, store.MediaFile{SizeBytes: ceilingSourceBytes}, streams) {
		t.Fatal("the viewer's ceiling vetoed an audio-only item, which has no picture to scale")
	}
	surround := []store.MediaFileStream{audioStream(0, "flac", 6)}
	forced := quirkOverlay{ForceTranscode: true}.withClientCeiling(2_000_000)
	if forced.ceilingBindsAudio(media, store.MediaFile{SizeBytes: ceilingSourceBytes}, surround) {
		t.Fatal("a ceiling downmixed an audio-only item that a device quirk transcodes")
	}
	video := store.Media{Class: store.MediaClassVideo, Container: "mkv", Codec: "h264", DurationSeconds: 120, VideoHeight: 1080}
	if !forced.ceilingBindsAudio(video, store.MediaFile{SizeBytes: ceilingSourceBytes}, []store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "truehd", 8)}) {
		t.Fatal("a ceiling-bound video transcode must hold audio to the rung's reserve")
	}
}

func TestStreamingCeilingSeparatesFingerprints(t *testing.T) {
	if CapabilityFingerprint(main10_1080p) == CapabilityFingerprint(withCeiling(main10_1080p, 5_000_000)) {
		t.Fatal("a ceiling must change the capability fingerprint so sessions do not share renditions across ceilings")
	}
}

// TestStreamingCeilingHoldsAudioToStereoAAC checks that a ceiling transcode
// does not pass lossless audio through: each rung reserves only 256 kbit/s
// for audio, so passthrough would push the stream past the ceiling.
func TestStreamingCeilingHoldsAudioToStereoAAC(t *testing.T) {
	profile := CapabilityProfile{
		Client:          "passthrough-tv",
		Containers:      []string{"mp4"},
		VideoCodecs:     []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
		AudioCodecs:     []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}, {Codec: "eac3", MaxChannels: 6}},
		DownstreamAudio: "passthrough",
	}
	audioGrant := func(t *testing.T, profile CapabilityProfile) streamtoken.Grant {
		t.Helper()
		service, st, scope := capabilityTestService(t)
		mediaID := importProbedMedia(t, st, "fp-ceiling-truehd", "mkv", "h264", 1920, 1080, []store.MediaFileStream{
			videoStream("h264", "High", 0, ""),
			audioStream(1, "truehd", 8),
		})
		resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
		if err != nil {
			t.Fatalf("grant: %v", err)
		}
		return grant
	}
	if got := audioGrant(t, profile).HLSAudioCodec; got != "copy" {
		t.Fatalf("without a ceiling HLSAudioCodec = %q, want copy: the fixture must pass audio through", got)
	}
	capped := audioGrant(t, withCeiling(profile, 2_000_000))
	if capped.HLSAudioCodec != "" || capped.HLSAudioChannels != 0 {
		t.Fatalf("under a ceiling audio = %q/%d, want stereo AAC", capped.HLSAudioCodec, capped.HLSAudioChannels)
	}
}

// TestStreamingCeilingNamesItselfForDolbyVisionP7 checks the dashboard reason
// for a P7 source whose HDR10 base layer would direct play without the
// ceiling: the ceiling refused it, not the HDR format.
func TestStreamingCeilingNamesItselfForDolbyVisionP7(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	p7 := videoStream("hevc", "Main 10", 7, "smpte2084")
	p7.DoViBLCompatID = doViBLCompatHDR10
	mediaID := importProbedMediaSized(t, st, "fp-ceiling-p7", "mkv", "hevc", 1920, 1080, ceilingSourceBytes, []store.MediaFileStream{
		p7,
		audioStream(1, "eac3", 6),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: withCeiling(main10_1080p, 5_000_000)})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Reason != bitrateCeilingReason {
		t.Fatalf("decision reason = %q, want %s", resp.Reason, bitrateCeilingReason)
	}
	obs, ok := service.PlayObservation(scope.SessionID, mediaID)
	if !ok || obs.Reason != PlayReasonBitrate {
		t.Fatalf("observation = %+v (%v), want reason %s", obs, ok, PlayReasonBitrate)
	}
}
