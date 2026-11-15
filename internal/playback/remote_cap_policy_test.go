package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// flacProfile direct plays stereo FLAC and accepts stereo AAC HLS.
var flacProfile = CapabilityProfile{
	Client:       "beamfall-test",
	Containers:   []string{"flac", "mp4"},
	DirectCodecs: []string{"flac", "aac"},
	AudioCodecs:  []AudioCodecCap{{Codec: "flac", MaxChannels: 2}, {Codec: "aac", MaxChannels: 2}},
}

// remoteCapAudioInfo runs one playbackInfo for an audio-only item after
// applying the admin's remote caps.
func remoteCapAudioInfo(t *testing.T, serverCap int, profile CapabilityProfile, remote bool, container string, sizeBytes int64, stream store.MediaFileStream) InfoResponse {
	t.Helper()
	service, st, scope := liveComposedService(t)
	setRemoteCaps(t, st, scope, serverCap, 0)
	path := filepath.Join(t.TempDir(), "track."+container)
	if err := os.WriteFile(path, []byte("beamfall audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "Track", Container: container, Codec: stream.Codec,
		Class: store.MediaClassAudio, ContentFingerprint: "fp-remote-cap-audio", DurationSecondsHint: 120,
		SizeBytes: sizeBytes, Streams: []store.MediaFileStream{stream},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(media.ID), CapabilityProfile: profile, RemoteClient: remote})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestRemotePolicyCapIsEnforcedAsConfigured covers CORE-1864: the admin's
// remote cap is not raised to the ladder floor and does not exempt audio-only
// items, while the viewer's own ceiling keeps both exceptions.
func TestRemotePolicyCapIsEnforcedAsConfigured(t *testing.T) {
	t.Run("a cap below the ladder floor refuses the video transcode", func(t *testing.T) {
		resp := remoteCapInfo(t, 1_000_000, 0, main10_1080p, true)
		if resp.Decision != "unavailable" || resp.Reason != policyBitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want unavailable/%s", resp.Decision, resp.Reason, policyBitrateCeilingReason)
		}
		for _, stream := range resp.Streams {
			for _, rendition := range stream.Renditions {
				if rendition.Bandwidth > 1_000_000 {
					t.Fatalf("rendition %+v exceeds the 1 Mbit/s cap", rendition)
				}
			}
		}
	})
	t.Run("a lower viewer ceiling does not raise the admin cap to the floor", func(t *testing.T) {
		resp := remoteCapInfo(t, 1_000_000, 0, withCeiling(main10_1080p, 500_000), true)
		if resp.Decision != "unavailable" || resp.Reason != policyBitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want unavailable/%s", resp.Decision, resp.Reason, policyBitrateCeilingReason)
		}
	})
	t.Run("a viewer ceiling below the floor still selects the lowest rung", func(t *testing.T) {
		resp := remoteCapInfo(t, 0, 0, withCeiling(main10_1080p, 1_000_000), true)
		if resp.Decision != "transcode" || resp.Reason != bitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, bitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, resp); top != ladderFloorBandwidth() {
			t.Fatalf("top rung %d bit/s, want the ladder floor %d", top, ladderFloorBandwidth())
		}
	})

	flac := store.MediaFileStream{Index: 0, Type: "audio", Codec: "flac", Channels: 2, SampleRate: 96_000, BitDepth: 24}
	// 90 MB over 120 s is a 6 Mbit/s source.
	t.Run("an audio-only item above the cap is not direct played", func(t *testing.T) {
		resp := remoteCapAudioInfo(t, 2_000_000, flacProfile, true, "flac", ceilingSourceBytes, flac)
		if resp.Decision == "direct-play" || resp.Decision == "direct-stream" {
			t.Fatalf("decision = %q/%q, want the 6 Mbit/s item held under the 2 Mbit/s cap", resp.Decision, resp.Reason)
		}
		if resp.Decision == "transcode" {
			if top := maxRenditionBandwidth(t, resp); top > 2_000_000 {
				t.Fatalf("top rendition %d bit/s exceeds the 2 Mbit/s cap", top)
			}
		}
	})
	t.Run("a lower viewer ceiling does not lift the admin cap off an audio-only item", func(t *testing.T) {
		resp := remoteCapAudioInfo(t, 2_000_000, withCeiling(flacProfile, 1_000_000), true, "flac", ceilingSourceBytes, flac)
		if resp.Decision == "direct-play" || resp.Decision == "direct-stream" {
			t.Fatalf("decision = %q/%q, want the 6 Mbit/s item held under the 2 Mbit/s cap", resp.Decision, resp.Reason)
		}
	})
	t.Run("the viewer's ceiling still exempts an audio-only item", func(t *testing.T) {
		resp := remoteCapAudioInfo(t, 0, withCeiling(flacProfile, 2_000_000), true, "flac", ceilingSourceBytes, flac)
		if resp.Decision != "direct-play" {
			t.Fatalf("decision = %q/%q, want direct-play", resp.Decision, resp.Reason)
		}
	})
	t.Run("an audio-only transcode above the cap drops to stereo AAC", func(t *testing.T) {
		passthrough := CapabilityProfile{
			Client: "beamfall-test", Containers: []string{"mp4"}, DirectCodecs: []string{"aac"}, DownstreamAudio: "passthrough",
			AudioCodecs: []AudioCodecCap{{Codec: "truehd", MaxChannels: 8}},
		}
		truehd := store.MediaFileStream{Index: 0, Type: "audio", Codec: "truehd", Channels: 8, SampleRate: 48_000, BitDepth: 24}
		loose := remoteCapAudioInfo(t, 0, passthrough, true, "mka", 0, truehd)
		if top := maxRenditionBandwidth(t, loose); top <= 4_000_000 {
			t.Fatalf("uncapped TrueHD rendition %d bit/s, want the passthrough peak above 4 Mbit/s", top)
		}
		capped := remoteCapAudioInfo(t, 4_000_000, passthrough, true, "mka", 0, truehd)
		if capped.Decision != "transcode" {
			t.Fatalf("decision = %q/%q, want transcode", capped.Decision, capped.Reason)
		}
		if top := maxRenditionBandwidth(t, capped); top > 4_000_000 {
			t.Fatalf("capped rendition %d bit/s exceeds the 4 Mbit/s cap", top)
		}
		// The grant leaves stereo AAC, the HLS default, unset.
		if audio := HLSAudioProfileFromGrant(hlsGrant(t, capped.Streams[0].Token)); audio.Codec != "" || audio.Channels != 0 {
			t.Fatalf("capped audio profile = %+v, want stereo AAC", audio)
		}
	})
}

func TestPolicyCeilingOverlay(t *testing.T) {
	media := store.Media{Class: store.MediaClassAudio, Container: "flac", Codec: "flac", DurationSeconds: 120}
	streams := []store.MediaFileStream{audioStream(0, "flac", 2)}
	file := store.MediaFile{SizeBytes: ceilingSourceBytes}
	if !(quirkOverlay{}).withPolicyCeiling(2_000_000).vetoesVideoPassthrough(media, file, streams) {
		t.Fatal("the admin's cap let a 6 Mbit/s audio-only item through")
	}
	if (quirkOverlay{}).withPolicyCeiling(8_000_000).vetoesVideoPassthrough(media, file, streams) {
		t.Fatal("the admin's cap vetoed a 6 Mbit/s audio-only item under it")
	}
	if got := (quirkOverlay{}).withPolicyCeiling(1_000_000).maxBitrate(); got != 1_000_000 {
		t.Fatalf("policy maxBitrate = %d, want the cap as configured", got)
	}
	both := quirkOverlay{}.withClientCeiling(3_000_000).withPolicyCeiling(2_000_000)
	if got := both.streamCeiling(); got != 2_000_000 {
		t.Fatalf("streamCeiling = %d, want the tighter admin cap", got)
	}
	if !both.policyCeiling {
		t.Fatal("a 2 Mbit/s cap under a 3 Mbit/s viewer ceiling does not name itself as binding")
	}
	if looser := (quirkOverlay{}).withClientCeiling(2_000_000).withPolicyCeiling(3_000_000); looser.policyCeiling || looser.streamCeiling() != 2_000_000 {
		t.Fatalf("looser cap overlay = %+v, want the viewer ceiling binding with the cap still held", looser)
	}
	// The viewer's ceiling exempts audio-only items, so the admin's cap names
	// itself for one even when the viewer's ceiling is tighter.
	tighterViewer := (quirkOverlay{}).withClientCeiling(1_000_000).withPolicyCeiling(2 * ladderFloorBandwidth())
	if got := tighterViewer.ceilingReason(media); got != policyBitrateCeilingReason {
		t.Fatalf("audio-only ceiling reason = %q, want %s", got, policyBitrateCeilingReason)
	}
	if got := tighterViewer.ceilingCause(media); got != PlayReasonPolicyBitrate {
		t.Fatalf("audio-only ceiling cause = %q, want %s", got, PlayReasonPolicyBitrate)
	}
	if got := tighterViewer.ceilingReason(store.Media{Class: store.MediaClassVideo}); got != bitrateCeilingReason {
		t.Fatalf("video ceiling reason = %q, want %s", got, bitrateCeilingReason)
	}
	if base := both.withoutClientCeiling(); base.streamCeiling() != 0 {
		t.Fatalf("withoutClientCeiling streamCeiling = %d, want 0", base.streamCeiling())
	}
	video := store.Media{Class: store.MediaClassVideo}
	if got := (quirkOverlay{}).withPolicyCeiling(1_000_000).policyTranscodeRefusal(video, 0); got != policyBitrateCeilingReason {
		t.Fatalf("refusal = %q, want %s below the ladder floor", got, policyBitrateCeilingReason)
	}
	if got := (quirkOverlay{}).withPolicyCeiling(4_000_000).policyTranscodeRefusal(video, 0); got != "" {
		t.Fatalf("refusal = %q, want none above the ladder floor", got)
	}
	if got := (quirkOverlay{}).withPolicyCeiling(100_000).policyTranscodeRefusal(media, 300_000); got != policyBitrateCeilingReason {
		t.Fatalf("refusal = %q, want %s for audio above the cap", got, policyBitrateCeilingReason)
	}
}
