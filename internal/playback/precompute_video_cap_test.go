package playback

import "testing"

// TestPrecomputeVideoKbpsExcludesAudioMuxReserve pins CORE-1846: a precomputed
// rung's encode caps video at the rung's video rate, so video plus the audio and
// mux reserve stays within the BANDWIDTH its master playlist advertises.
func TestPrecomputeVideoKbpsExcludesAudioMuxReserve(t *testing.T) {
	rungs := append(append([]Rung{}, h264LadderRungs...), hevcLadderRungs...)
	for _, rung := range rungs {
		advertised := bandwidthFor(rung.VideoKbps)
		got := precomputeVideoKbps(advertised)
		if got != rung.VideoKbps {
			t.Errorf("%s: precomputeVideoKbps(%d) = %d, want %d", rung.ID, advertised, got, rung.VideoKbps)
		}
		if peak := got*1000 + audioMuxReserve; peak > advertised {
			t.Errorf("%s: video %d kbps + reserve = %d, above advertised BANDWIDTH %d", rung.ID, got, peak, advertised)
		}
	}
}

func TestPrecomputeRequestRefusesBandwidthWithinReserve(t *testing.T) {
	req := PrecomputeRequest{
		MediaID:            "media-alpha",
		ContentFingerprint: "sha256:alpha",
		RenditionID:        "spec-720p",
		RungID:             "720p",
		Codec:              "h264",
		Height:             720,
		SourcePath:         "/library/alpha.mkv",
		Path:               "/cache/alpha",
	}
	for _, bandwidth := range []int{1, audioMuxReserve, audioMuxReserve + 999} {
		req.Bandwidth = bandwidth
		if err := validatePrecomputeRequest(req); err == nil {
			t.Errorf("bandwidth %d accepted, want refusal: no room for video after the reserve", bandwidth)
		}
	}
	req.Bandwidth = bandwidthFor(4000)
	if err := validatePrecomputeRequest(req); err != nil {
		t.Fatalf("bandwidthFor(4000) refused: %v", err)
	}
}
