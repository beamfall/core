package playback

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/store"
)

// seedAudio imports an audio-class item into the session-visible local library
// and returns its ID. The bytes back a real file so direct-play negotiation runs
// the production path.
func seedAudio(t *testing.T, service *Service, fingerprint string) store.MediaID {
	t.Helper()
	audioPath := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(audioPath, []byte("beamfall audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	audio, _, err := service.store.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               audioPath,
		Title:              "Sample Song",
		Container:          "mp3",
		Codec:              "mp3",
		ContentFingerprint: fingerprint,
		Class:              store.MediaClassAudio,
	})
	if err != nil {
		t.Fatalf("ImportMediaFile(audio): %v", err)
	}
	return audio.ID
}

var r128Measurement = LoudnessData{
	IntegratedLUFS:  -23.0,
	LoudnessRangeLU: 6.0,
	TruePeakDBTP:    -1.0,
	ReferenceLUFS:   -18.0,
	GainDB:          -5.0,
	Source:          LoudnessDataSourceR128,
}

// TestLoudnessDirectSupported: a direct-play stream + a client that DECLARED the
// matching normalization mode → direct-supported. The client applies GainDB
// itself; the rung is NOT downgraded to normalize (ADR-0098 area 3). State is
// derived from the declared mode, asserted independent of the client name.
func TestLoudnessDirectSupported(t *testing.T) {
	// feature:loudness feature:capability-reporting
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	id := seedAudio(t, service, "sha256:loudness-direct-supported")
	m := r128Measurement
	service.SetAssets(fakeAssetsLocator{loudness: &m})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(id),
		CapabilityProfile: CapabilityProfile{
			// A client name that, under the old client-name-inference world, would
			// say nothing — the state must come from the DECLARED mode only.
			Client:             "unknown-client",
			Containers:         []string{"mp3"},
			DirectCodecs:       []string{"mp3"},
			NormalizationModes: []string{NormalizationModeR128},
		},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("decision = %q, want direct-play (precondition for this state)", resp.Decision)
	}
	if resp.Loudness == nil {
		t.Fatal("Loudness is nil, want a report")
	}
	if resp.Loudness.State != LoudnessStateDirectSupported {
		t.Fatalf("state = %q, want %q", resp.Loudness.State, LoudnessStateDirectSupported)
	}
	if resp.Loudness.GainDB != -5.0 || resp.Loudness.IntegratedLUFS != -23.0 {
		t.Fatalf("measurement = %+v, want the seeded values", resp.Loudness)
	}
	// The direct-play stream is preserved — the server did not force a transcode
	// or a worse rung to normalize.
	if len(resp.Streams) == 0 || resp.Streams[0].Kind != "direct-play" {
		t.Fatalf("streams = %+v, want a direct-play stream preserved", resp.Streams)
	}
	if !strings.HasSuffix(resp.Loudness.AssetURL, "/loudness.json") {
		t.Fatalf("assetURL = %q, want a token-bearing loudness.json URL", resp.Loudness.AssetURL)
	}
}

// TestLoudnessPassthroughUnsupported: a direct-play stream + a client that did
// NOT declare a covering mode → passthrough-unsupported. The server reports the
// honest state and still does NOT force a transcode/worse rung to normalize.
func TestLoudnessPassthroughUnsupported(t *testing.T) {
	// feature:loudness feature:capability-reporting
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	id := seedAudio(t, service, "sha256:loudness-passthrough")
	m := r128Measurement
	service.SetAssets(fakeAssetsLocator{loudness: &m})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(id),
		CapabilityProfile: CapabilityProfile{
			// Direct-playable but declares NO normalization capability.
			Containers:   []string{"mp3"},
			DirectCodecs: []string{"mp3"},
		},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision != "direct-play" {
		t.Fatalf("decision = %q, want direct-play", resp.Decision)
	}
	if resp.Loudness == nil || resp.Loudness.State != LoudnessStatePassthroughUnsupported {
		t.Fatalf("state = %+v, want %q", resp.Loudness, LoudnessStatePassthroughUnsupported)
	}
	// Crucially: no forced transcode — the direct-play stream stands.
	if len(resp.Streams) == 0 || resp.Streams[0].Kind != "direct-play" {
		t.Fatalf("streams = %+v, want the direct-play stream preserved (no forced normalize-transcode)", resp.Streams)
	}
	// The measured numbers are still reported so a client could normalize later.
	if resp.Loudness.GainDB != -5.0 {
		t.Fatalf("gainDB = %v, want the measurement still reported", resp.Loudness.GainDB)
	}
}

// TestLoudnessServerNormalized: a stream the server was ALREADY going to
// transcode for other reasons (unsupported codec) → server-normalized; the
// server applies the offset in that transcode.
func TestLoudnessServerNormalized(t *testing.T) {
	// feature:loudness feature:capability-reporting
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	id := seedAudio(t, service, "sha256:loudness-server-normalized")
	m := r128Measurement
	service.SetAssets(fakeAssetsLocator{loudness: &m})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(id),
		CapabilityProfile: CapabilityProfile{
			// mp3 is NOT in DirectCodecs → the item cannot direct-play and the
			// server transcodes for codec reasons, independent of loudness.
			Containers:         []string{"mp4"},
			DirectCodecs:       []string{"aac"},
			NormalizationModes: []string{NormalizationModeR128},
		},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Decision == "direct-play" {
		t.Fatalf("decision = %q, want a non-direct (transcode) verdict for this state", resp.Decision)
	}
	if resp.Loudness == nil || resp.Loudness.State != LoudnessStateServerNormalized {
		t.Fatalf("state = %+v, want %q", resp.Loudness, LoudnessStateServerNormalized)
	}
}

// TestLoudnessUnavailable: no stored measurement → no loudness block at all (the
// omitempty default), distinct from a present report. A client that declares a
// mode but has no analysis yet gets nothing rather than a fabricated state.
func TestLoudnessUnavailable(t *testing.T) {
	// feature:loudness feature:capability-reporting
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	id := seedAudio(t, service, "sha256:loudness-unavailable")
	// No loudness on the locator → LoudnessForFingerprint reports absent.
	service.SetAssets(fakeAssetsLocator{trickplay: true})

	resp, err := service.Info(context.Background(), scope, InfoRequest{
		MediaID: string(id),
		CapabilityProfile: CapabilityProfile{
			Containers:         []string{"mp3"},
			DirectCodecs:       []string{"mp3"},
			NormalizationModes: []string{NormalizationModeR128},
		},
	})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if resp.Loudness != nil {
		t.Fatalf("Loudness = %+v, want nil (unavailable → no block)", resp.Loudness)
	}
}

// TestLoudnessTagSourceMatchesReplayGainMode: a tags-sourced measurement is
// covered by the "replaygain" declared mode, not "r128" — proving the
// Source→mode match drives the state, not the client name.
func TestLoudnessTagSourceMatchesReplayGainMode(t *testing.T) {
	// feature:loudness feature:capability-reporting
	service, scope := newTestServiceWithMediaFile(t, "session-default", "media-alpha")
	id := seedAudio(t, service, "sha256:loudness-tag-source")
	m := r128Measurement
	m.Source = LoudnessDataSourceTags
	service.SetAssets(fakeAssetsLocator{loudness: &m})

	base := CapabilityProfile{Containers: []string{"mp3"}, DirectCodecs: []string{"mp3"}}

	// Declaring only "r128" does NOT cover a tags-sourced measurement.
	r128Only := base
	r128Only.NormalizationModes = []string{NormalizationModeR128}
	resp, err := service.Info(context.Background(), scope, InfoRequest{MediaID: string(id), CapabilityProfile: r128Only})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Loudness == nil || resp.Loudness.State != LoudnessStatePassthroughUnsupported {
		t.Fatalf("r128-only on a tag source: state = %+v, want passthrough-unsupported", resp.Loudness)
	}

	// Declaring "replaygain" DOES cover it.
	rg := base
	rg.NormalizationModes = []string{NormalizationModeReplayGain}
	resp, err = service.Info(context.Background(), scope, InfoRequest{MediaID: string(id), CapabilityProfile: rg})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Loudness == nil || resp.Loudness.State != LoudnessStateDirectSupported {
		t.Fatalf("replaygain on a tag source: state = %+v, want direct-supported", resp.Loudness)
	}
}
