package playback

import (
	"context"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// remoteCapInfo runs one playbackInfo for a probed 1080p HEVC source after
// applying the admin's remote caps (server-wide and per Profile, 0 = none).
func remoteCapInfo(t *testing.T, serverCap, profileCap int, profile CapabilityProfile, remote bool) InfoResponse {
	t.Helper()
	service, st, scope := capabilityTestService(t)
	setRemoteCaps(t, st, scope, serverCap, profileCap)
	mediaID := importProbedMediaSized(t, st, "fp-remote-cap", "mkv", "hevc", 1920, 1080, ceilingSourceBytes, []store.MediaFileStream{
		videoStream("hevc", "Main 10", 0, ""),
		audioStream(1, "eac3", 6),
	})
	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID:           string(mediaID),
		CapabilityProfile: profile,
		RemoteClient:      remote,
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return resp
}

func setRemoteCaps(t *testing.T, st *store.Store, scope auth.Scope, serverCap, profileCap int) {
	t.Helper()
	ctx := context.Background()
	if serverCap > 0 {
		if _, err := st.SetConsoleConfig(ctx, store.ConsoleConfigPatch{RemoteMaxBitrate: &serverCap}, time.Unix(100, 0)); err != nil {
			t.Fatalf("SetConsoleConfig: %v", err)
		}
	}
	if profileCap > 0 {
		if _, err := st.UpdateProfileSettings(ctx, scope.ProfileID, store.ProfileSettings{RemoteMaxBitrate: &profileCap}); err != nil {
			t.Fatalf("UpdateProfileSettings: %v", err)
		}
	}
}

func maxRenditionBandwidth(t *testing.T, resp InfoResponse) int {
	t.Helper()
	if len(resp.Streams) == 0 || len(resp.Streams[0].Renditions) == 0 {
		t.Fatalf("no renditions advertised: %+v", resp.Streams)
	}
	top := 0
	for _, rendition := range resp.Streams[0].Renditions {
		top = max(top, rendition.Bandwidth)
	}
	return top
}

// TestRemoteBitrateCapDecision covers CORE-1717: a remote request plays
// under the lower of the client's ceiling and the admin's remote cap, and a
// LAN request ignores the admin's remote cap.
func TestRemoteBitrateCapDecision(t *testing.T) {
	t.Run("LAN request ignores the remote cap", func(t *testing.T) {
		resp := remoteCapInfo(t, 4_000_000, 3_000_000, main10_1080p, false)
		if resp.Decision != "direct-play" {
			t.Fatalf("decision = %q/%q, want direct-play", resp.Decision, resp.Reason)
		}
	})
	t.Run("remote request with no cap configured direct plays", func(t *testing.T) {
		resp := remoteCapInfo(t, 0, 0, main10_1080p, true)
		if resp.Decision != "direct-play" {
			t.Fatalf("decision = %q/%q, want direct-play", resp.Decision, resp.Reason)
		}
	})
	t.Run("server-wide cap transcodes a remote request under it", func(t *testing.T) {
		resp := remoteCapInfo(t, 4_000_000, 0, main10_1080p, true)
		if resp.Decision != "transcode" || resp.Reason != policyBitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, policyBitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, resp); top > 4_000_000 {
			t.Fatalf("top rung %d bit/s exceeds the 4 Mbit/s server cap", top)
		}
	})
	t.Run("per-Profile cap below the server cap wins", func(t *testing.T) {
		resp := remoteCapInfo(t, 8_000_000, 2_000_000, main10_1080p, true)
		if resp.Reason != policyBitrateCeilingReason {
			t.Fatalf("reason = %q, want %s", resp.Reason, policyBitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, resp); top > 2_000_000 {
			t.Fatalf("top rung %d bit/s exceeds the 2 Mbit/s Profile cap", top)
		}
	})
	t.Run("server cap below the per-Profile cap wins", func(t *testing.T) {
		resp := remoteCapInfo(t, 2_000_000, 8_000_000, main10_1080p, true)
		if top := maxRenditionBandwidth(t, resp); top > 2_000_000 {
			t.Fatalf("top rung %d bit/s exceeds the 2 Mbit/s server cap", top)
		}
	})
	t.Run("policy cap below the client ceiling binds", func(t *testing.T) {
		loose := remoteCapInfo(t, 0, 0, withCeiling(main10_1080p, 8_000_000), true)
		capped := remoteCapInfo(t, 2_000_000, 0, withCeiling(main10_1080p, 8_000_000), true)
		if capped.Reason != policyBitrateCeilingReason {
			t.Fatalf("reason = %q, want %s", capped.Reason, policyBitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, capped); top > 2_000_000 || top >= maxRenditionBandwidth(t, loose) {
			t.Fatalf("top rung %d bit/s, want under the 2 Mbit/s policy cap and below the client-only ladder", top)
		}
	})
	t.Run("client ceiling below the policy cap keeps its own reason", func(t *testing.T) {
		resp := remoteCapInfo(t, 8_000_000, 0, withCeiling(main10_1080p, 2_000_000), true)
		if resp.Decision != "transcode" || resp.Reason != bitrateCeilingReason {
			t.Fatalf("decision = %q/%q, want transcode/%s", resp.Decision, resp.Reason, bitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, resp); top > 2_000_000 {
			t.Fatalf("top rung %d bit/s exceeds the 2 Mbit/s client ceiling", top)
		}
	})
	t.Run("LAN request keeps the client ceiling only", func(t *testing.T) {
		resp := remoteCapInfo(t, 2_000_000, 0, withCeiling(main10_1080p, 8_000_000), false)
		if resp.Reason != bitrateCeilingReason {
			t.Fatalf("reason = %q, want %s", resp.Reason, bitrateCeilingReason)
		}
		if top := maxRenditionBandwidth(t, resp); top <= 2_000_000 {
			t.Fatalf("top rung %d bit/s, want the 8 Mbit/s client ladder, not the remote cap", top)
		}
	})
	t.Run("another refusal keeps its own reason", func(t *testing.T) {
		h264Only := main10_1080p
		h264Only.VideoCodecs = []VideoCodecCap{{Codec: "h264", Profiles: []string{"High", "Main"}, MaxHeight: 1080}}
		resp := remoteCapInfo(t, 4_000_000, 0, h264Only, true)
		if resp.Decision != "transcode" || resp.Reason != "requires-transcode" {
			t.Fatalf("decision = %q/%q, want transcode/requires-transcode", resp.Decision, resp.Reason)
		}
	})
}
