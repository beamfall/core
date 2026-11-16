package playback

import (
	"slices"
	"testing"
)

// h264Only1080p cannot direct play the HEVC ceiling fixture, so it always
// transcodes onto the full h264 ladder.
func h264Only1080p() CapabilityProfile {
	profile := main10_1080p
	profile.VideoCodecs = []VideoCodecCap{{Codec: "h264", Profiles: []string{"High", "Main"}, MaxHeight: 1080}}
	return profile
}

func renditionIDs(resp InfoResponse) []string {
	if len(resp.Streams) == 0 {
		return nil
	}
	ids := make([]string, 0, len(resp.Streams[0].Renditions))
	for _, rendition := range resp.Streams[0].Renditions {
		ids = append(ids, rendition.ID)
	}
	return ids
}

// TestViewerQualityStepIsHonouredBelowEveryCap covers CORE-1971: a client
// quality picker sends an advertised rendition's bandwidth as its ceiling.
// That step must top out at that rendition, never above it or above a tighter
// admin cap, and no ceiling (Auto) must negotiate as before.
func TestViewerQualityStepIsHonouredBelowEveryCap(t *testing.T) {
	_, auto, _, _ := ceilingInfo(t, h264Only1080p(), ceilingSourceBytes)
	if auto.Decision != "transcode" || auto.Reason != "requires-transcode" {
		t.Fatalf("Auto decision = %q/%q, want transcode/requires-transcode", auto.Decision, auto.Reason)
	}
	if got, want := renditionIDs(auto), []string{"1080p", "720p", "480p"}; !slices.Equal(got, want) {
		t.Fatalf("Auto renditions = %v, want the full ladder %v", got, want)
	}

	t.Run("Auto direct plays what it direct played before", func(t *testing.T) {
		_, resp, _, _ := ceilingInfo(t, withCeiling(main10_1080p, 0), ceilingSourceBytes)
		if resp.Decision != "direct-play" || resp.Reason != "" {
			t.Fatalf("decision = %q/%q, want direct-play with no reason", resp.Decision, resp.Reason)
		}
	})

	for _, step := range auto.Streams[0].Renditions {
		t.Run("step "+step.ID+" tops out at itself", func(t *testing.T) {
			_, resp, _, _ := ceilingInfo(t, withCeiling(h264Only1080p(), step.Bandwidth), ceilingSourceBytes)
			ids := renditionIDs(resp)
			if len(ids) == 0 || ids[0] != step.ID {
				t.Fatalf("renditions = %v, want %s on top", ids, step.ID)
			}
			if top := maxRenditionBandwidth(t, resp); top != step.Bandwidth {
				t.Fatalf("top rendition %d bit/s, want the step's %d", top, step.Bandwidth)
			}
		})
		t.Run("step "+step.ID+" never exceeds a tighter admin cap", func(t *testing.T) {
			const profileCap = 3_000_000
			resp := remoteCapInfo(t, 0, profileCap, withCeiling(h264Only1080p(), step.Bandwidth), true)
			if top := maxRenditionBandwidth(t, resp); top > min(profileCap, step.Bandwidth) {
				t.Fatalf("top rendition %d bit/s exceeds min(step %d, cap %d)", top, step.Bandwidth, profileCap)
			}
		})
	}
}
