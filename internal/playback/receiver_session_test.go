package playback

import (
	"context"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestCapabilityFingerprintSeparatesReceiverSessionKey(t *testing.T) {
	sender := CapabilityProfile{
		Client:          "beamfall-ios",
		Containers:      []string{"mp4"},
		DirectCodecs:    []string{"h264", "hevc"},
		SubtitleFormats: []string{"webvtt", "ass"},
	}
	receiver := CapabilityProfile{
		Client:          "chromecast-gen2",
		Containers:      []string{"mp4"},
		DirectCodecs:    []string{"h264"},
		SubtitleFormats: []string{"webvtt"},
	}
	senderKey := SessionKey{
		SessionID:         "session-one",
		MediaID:           "media-one",
		ProfileCapability: CapabilityFingerprint(sender),
	}
	receiverKey := SessionKey{
		SessionID:         "session-one",
		MediaID:           "media-one",
		ProfileCapability: CapabilityFingerprint(receiver),
	}
	if senderKey.ProfileCapability == "" || receiverKey.ProfileCapability == "" {
		t.Fatalf("capability fingerprints = %q / %q, want non-empty", senderKey.ProfileCapability, receiverKey.ProfileCapability)
	}
	if senderKey == receiverKey {
		t.Fatal("receiver route change reused the sender session key")
	}
}

func TestCapabilityFingerprintStableForIdenticalProfiles(t *testing.T) {
	// Two independently constructed values with equal contents and separate
	// backing arrays: fingerprint equality must follow from the capability
	// values, never from sharing the same slice headers.
	profile := CapabilityProfile{
		Client:       "beamfall-web",
		Containers:   []string{"mp4"},
		DirectCodecs: []string{"h264"},
	}
	identical := CapabilityProfile{
		Client:       "beamfall-web",
		Containers:   []string{"mp4"},
		DirectCodecs: []string{"h264"},
	}
	if CapabilityFingerprint(profile) != CapabilityFingerprint(identical) {
		t.Fatal("identical profiles produced different capability fingerprints")
	}
	if got := CapabilityFingerprint(CapabilityProfile{}); got != "" {
		t.Fatalf("empty profile fingerprint = %q, want legacy empty identity", got)
	}
}

func TestCapabilityFingerprintRuntimeDowngradeCreatesNewSession(t *testing.T) {
	full := CapabilityProfile{
		Client:          "beamfall-web",
		Containers:      []string{"mp4"},
		SubtitleFormats: []string{"webvtt", "ass"},
	}
	downgraded := full
	downgraded.SubtitleFormats = []string{"webvtt"}
	if CapabilityFingerprint(full) == CapabilityFingerprint(downgraded) {
		t.Fatal("runtime capability downgrade did not change session identity")
	}
}

func TestReceiverProfileGrantCarriesQuirkResolvedCapability(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	installQuirks(t, service,
		QuirkEntry{
			ID:       "chromecast-gen2-height",
			Note:     "fixture receiver ceiling",
			Selector: QuirkSelector{Client: "chromecast-gen2"},
			Override: QuirkOverride{MaxHeight: 480},
		},
		QuirkEntry{
			ID:       "appletv-airplay-height",
			Note:     "fixture receiver ceiling",
			Selector: QuirkSelector{Client: "appletv-airplay"},
			Override: QuirkOverride{MaxHeight: 480},
		},
	)
	for _, tc := range []struct {
		client string
		quirk  string
	}{
		{client: "chromecast-gen2", quirk: "chromecast-gen2-height"},
		{client: "appletv-airplay", quirk: "appletv-airplay-height"},
	} {
		t.Run(tc.client, func(t *testing.T) {
			mediaID := importQuirkMedia(t, st, "fp-"+tc.client)
			profile := quirkClientProfile
			profile.Client = tc.client

			resp, err := service.Info(context.Background(), scope, InfoRequest{
				MediaID:           string(mediaID),
				CapabilityProfile: profile,
			})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if !slices.Contains(resp.AppliedQuirks, tc.quirk) {
				t.Fatalf("AppliedQuirks = %v, want %q", resp.AppliedQuirks, tc.quirk)
			}
			if len(resp.Streams) != 1 || resp.Streams[0].Kind != "hls" {
				t.Fatalf("Streams = %#v, want quirk-resolved HLS choice", resp.Streams)
			}
			grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("UnverifiedGrant: %v", err)
			}
			if want := CapabilityFingerprint(profile); grant.ProfileCapability != want {
				t.Fatalf("grant ProfileCapability = %q, want %q", grant.ProfileCapability, want)
			}
			if !grant.ReceiverBound {
				t.Fatal("receiver grant is not marked receiver-bound")
			}
			if !grant.ReceiverProfileResolved {
				t.Fatal("receiver grant does not carry selector-resolution proof")
			}
			if grant.HLSClientMaxHeight != 480 {
				t.Fatalf("grant HLSClientMaxHeight = %d, want receiver quirk ceiling 480", grant.HLSClientMaxHeight)
			}
		})
	}
}

// TestShippedBaselineResolvesDefaultMediaReceiver pins CORE-2563: with only
// the release-embedded quirks database, a sender casting to the Default Media
// Receiver gets a selector-resolved, receiver-bound grant, while any other
// chromecast- client stays unresolved so the byte routes keep refusing it.
func TestShippedBaselineResolvesDefaultMediaReceiver(t *testing.T) {
	service, st, scope := capabilityTestService(t)
	targets := map[string]streamtoken.Target{"hls": streamtoken.TargetMediaHLS, "direct": streamtoken.TargetMediaDirect}
	for _, tc := range []struct {
		client   string
		resolved bool
	}{
		{client: "chromecast-default-receiver", resolved: true},
		{client: "chromecast-unknown", resolved: false},
	} {
		t.Run(tc.client, func(t *testing.T) {
			mediaID := importQuirkMedia(t, st, "fp-baseline-"+tc.client)
			profile := CapabilityProfile{
				Client:          tc.client,
				Containers:      []string{"mp4"},
				DirectCodecs:    []string{"h264"},
				VideoCodecs:     []VideoCodecCap{{Codec: "h264", MaxHeight: 1080}},
				AudioCodecs:     []AudioCodecCap{{Codec: "aac", MaxChannels: 2}},
				SubtitleFormats: []string{"webvtt"},
				DownstreamAudio: "stereo",
			}
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: profile})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if got := slices.Contains(resp.AppliedQuirks, "chromecast-default-receiver-floor"); got != tc.resolved {
				t.Fatalf("AppliedQuirks = %v, want floor applied %v", resp.AppliedQuirks, tc.resolved)
			}
			if len(resp.Streams) == 0 {
				t.Fatal("no stream offered")
			}
			target, ok := targets[resp.Streams[0].Kind]
			if !ok {
				t.Fatalf("unexpected stream kind %q", resp.Streams[0].Kind)
			}
			grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, target)
			if err != nil {
				t.Fatalf("UnverifiedGrant: %v", err)
			}
			if !grant.ReceiverBound {
				t.Fatal("cast grant is not receiver-bound")
			}
			if grant.ReceiverProfileResolved != tc.resolved {
				t.Fatalf("ReceiverProfileResolved = %v, want %v", grant.ReceiverProfileResolved, tc.resolved)
			}
		})
	}
}

// TestShippedBaselineHoldsBroadSenderToTheCastFloor pins the Codex finding on
// CORE-2563: a sender whose own profile is broader than the floor must still
// get AAC, stereo and at most 720 lines for the Default Media Receiver, on the
// direct-play path and on the transcode path.
func TestShippedBaselineHoldsBroadSenderToTheCastFloor(t *testing.T) {
	broad := CapabilityProfile{
		Client:      "chromecast-default-receiver",
		Containers:  []string{"mkv", "mp4"},
		VideoCodecs: []VideoCodecCap{{Codec: "hevc", Profiles: []string{"Main 10"}, MaxHeight: 2160}, {Codec: "h264", MaxHeight: 2160}},
		AudioCodecs: []AudioCodecCap{{Codec: "eac3", MaxChannels: 6}, {Codec: "ac3", MaxChannels: 6}, {Codec: "aac", MaxChannels: 6}},
		HDRFormats:  []string{"hdr10", "hlg"},
	}
	for _, tc := range []struct {
		name    string
		codec   string
		height  int
		streams []store.MediaFileStream
	}{
		{name: "H.264 720p with stereo E-AC-3", codec: "h264", height: 720, streams: []store.MediaFileStream{videoStream("h264", "High", 0, ""), audioStream(1, "eac3", 2)}},
		{name: "HEVC 1080p with 5.1 E-AC-3", codec: "hevc", height: 1080, streams: []store.MediaFileStream{videoStream("hevc", "Main 10", 0, ""), audioStream(1, "eac3", 6)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, st, scope := capabilityTestService(t)
			mediaID := importProbedMedia(t, st, "fp-cast-floor-"+tc.codec, "mkv", tc.codec, tc.height*16/9, tc.height, tc.streams)
			resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(mediaID), CapabilityProfile: broad})
			if err != nil {
				t.Fatalf("Info: %v", err)
			}
			if !slices.Contains(resp.AppliedQuirks, "chromecast-default-receiver-floor") {
				t.Fatalf("AppliedQuirks = %v, want the Cast floor", resp.AppliedQuirks)
			}
			if resp.Decision == "direct-play" {
				t.Fatal("a source with E-AC-3 audio direct-played to the Default Media Receiver")
			}
			if len(resp.Streams) == 0 || resp.Streams[0].Kind != "hls" {
				t.Fatalf("streams = %+v, want an HLS stream first", resp.Streams)
			}
			grant, err := service.tokens.UnverifiedGrant(resp.Streams[0].Token, streamtoken.TargetMediaHLS)
			if err != nil {
				t.Fatalf("UnverifiedGrant: %v", err)
			}
			audio := HLSAudioProfileFromGrant(grant)
			// The zero AudioProfile is the encoder's AAC stereo default.
			if (audio.Codec != "" && audio.Codec != "aac") || audio.Channels > 2 {
				t.Fatalf("HLS audio = %q x%d, want AAC with at most two channels", audio.Codec, audio.Channels)
			}
			// AudioCopy is the audio-only transcode: the video is copied through,
			// so it keeps the source picture, which must already sit in the floor.
			if grant.AudioCopy {
				if tc.codec != "h264" || tc.height > 720 {
					t.Fatalf("video copied through for a %s %dp source, outside the H.264 720p floor", tc.codec, tc.height)
				}
				return
			}
			if caps := LadderCapsFromGrant(grant); caps.ClientMaxHeight == 0 || caps.ClientMaxHeight > 720 || caps.HEVC {
				t.Fatalf("HLS ladder caps = %+v, want H.264 at most 720 lines", caps)
			}
		})
	}
}
