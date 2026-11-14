package playback

import (
	"context"
	"testing"

	"github.com/beamfall/core/internal/store"
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
}

func TestStreamingCeilingSeparatesFingerprints(t *testing.T) {
	if CapabilityFingerprint(main10_1080p) == CapabilityFingerprint(withCeiling(main10_1080p, 5_000_000)) {
		t.Fatal("a ceiling must change the capability fingerprint so sessions do not share renditions across ceilings")
	}
}
