package genassets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateLoudnessFromR128 covers the ebur128-analysis path (ADR-0098 area
// 3): a source with no trusted tag is measured, the analysis is persisted keyed
// by fingerprint, GainDB drives the integrated loudness toward the -18 LUFS
// reference, and the Source records "r128" provenance.
func TestGenerateLoudnessFromR128(t *testing.T) {
	// feature:loudness feature:generated-assets
	gen := &fakeGen{loudnessIntegrated: -23.4, loudnessRange: 6.2, loudnessTruePeak: -1.1, loudnessFromTags: false}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio one"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateLoudness(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateLoudness returned error: %v", err)
	}
	analysis := readLoudness(t, svc, fingerprint)
	if analysis.Version != loudnessVersion {
		t.Fatalf("version = %d, want %d", analysis.Version, loudnessVersion)
	}
	if analysis.Source != LoudnessSourceR128 {
		t.Fatalf("source = %q, want %q (computed, not tagged)", analysis.Source, LoudnessSourceR128)
	}
	if analysis.IntegratedLUFS != -23.4 || analysis.LoudnessRangeLU != 6.2 || analysis.TruePeakDBTP != -1.1 {
		t.Fatalf("measurement = %+v, want the seeded values", analysis)
	}
	if analysis.ReferenceLUFS != loudnessReferenceLUFS {
		t.Fatalf("reference = %v, want %v", analysis.ReferenceLUFS, loudnessReferenceLUFS)
	}
	// GainDB = reference - integrated = -18 - (-23.4) = +5.4 dB (toward the target).
	if got, want := analysis.GainDB, loudnessReferenceLUFS-(-23.4); abs(got-want) > 1e-9 {
		t.Fatalf("gainDB = %v, want %v", got, want)
	}
	// Servable through the same fingerprint-keyed asset route as every sibling.
	if name, ok := svc.LoudnessAsset(fingerprint); !ok || name != LoudnessName {
		t.Fatalf("LoudnessAsset = (%q, %v), want (%q, true)", name, ok, LoudnessName)
	}
	if _, err := svc.AssetPath(fingerprint, LoudnessName); err != nil {
		t.Fatalf("loudness not servable: %v", err)
	}
	// The content seam returns the parsed measurement for the playback report.
	data, ok := svc.LoudnessForFingerprint(fingerprint)
	if !ok || data.Source != LoudnessSourceR128 || data.GainDB != analysis.GainDB {
		t.Fatalf("LoudnessForFingerprint = (%+v, %v), want the parsed analysis", data, ok)
	}
}

// TestGenerateLoudnessFromTags covers the trusted-tag fast path (ADR-0098 area
// 3): a measurement whose provenance is the container's ReplayGain/R128 tags is
// recorded with Source "tags".
func TestGenerateLoudnessFromTags(t *testing.T) {
	// feature:loudness feature:generated-assets
	gen := &fakeGen{loudnessIntegrated: -16.0, loudnessRange: 4.0, loudnessTruePeak: -0.5, loudnessFromTags: true}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("tagged audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Tagged Track")

	if err := svc.GenerateLoudness(context.Background(), media.ID); err != nil {
		t.Fatalf("GenerateLoudness returned error: %v", err)
	}
	analysis := readLoudness(t, svc, fingerprint)
	if analysis.Source != LoudnessSourceTags {
		t.Fatalf("source = %q, want %q (trusted tags)", analysis.Source, LoudnessSourceTags)
	}
}

// TestGenerateLoudnessIdempotentPerFingerprint guards the regeneration key: an
// existing measurement for the same fingerprint is never recomputed (a metadata
// edit keeps the same bytes/fingerprint/directory), while a quality-upgrade
// replacement (new bytes → new fingerprint) regenerates.
func TestGenerateLoudnessIdempotentPerFingerprint(t *testing.T) {
	// feature:loudness feature:generated-assets
	gen := &fakeGen{loudnessIntegrated: -20.0}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _ := seedAudioMedia(t, st, track, "Track One")

	if err := svc.GenerateLoudness(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.GenerateLoudness(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	if gen.loudnessCalls != 1 {
		t.Fatalf("LoudnessMeasure called %d times, want 1 (idempotent per fingerprint)", gen.loudnessCalls)
	}

	// Replace the bytes → new fingerprint → a fresh measurement is computed.
	if err := os.WriteFile(track, []byte("audio v2 upgraded"), 0o600); err != nil {
		t.Fatal(err)
	}
	media2, _ := seedAudioMedia(t, st, track, "Track One Remaster")
	if err := svc.GenerateLoudness(context.Background(), media2.ID); err != nil {
		t.Fatal(err)
	}
	if gen.loudnessCalls != 2 {
		t.Fatalf("LoudnessMeasure called %d times after re-fingerprint, want 2 (stale source regenerates)", gen.loudnessCalls)
	}
}

// TestGenerateLoudnessSkipsVideo guards the audio-class gate: loudness is a
// generic audio property, so a video-class item is a no-op (no analysis, no
// asset) — never an error.
func TestGenerateLoudnessSkipsVideo(t *testing.T) {
	// feature:loudness feature:generated-assets
	gen := &fakeGen{loudnessIntegrated: -20.0}
	// media-alpha is video-class (newTestService seeds video).
	svc, _, fingerprint := newTestService(t, gen)
	if err := svc.GenerateLoudness(context.Background(), "media-alpha"); err != nil {
		t.Fatalf("GenerateLoudness on video returned error: %v", err)
	}
	if gen.loudnessCalls != 0 {
		t.Fatalf("LoudnessMeasure called %d times on a video item, want 0 (audio-class gate)", gen.loudnessCalls)
	}
	if _, ok := svc.LoudnessAsset(fingerprint); ok {
		t.Fatal("video item got a loudness asset; audio-class gate failed")
	}
}

// TestGenerateLoudnessClampsGain guards the gain clamp: a wildly mis-tagged
// source cannot drive an absurd normalization offset.
func TestGenerateLoudnessClampsGain(t *testing.T) {
	// feature:loudness feature:generated-assets
	gen := &fakeGen{loudnessIntegrated: -60.0} // reference - (-60) = +42 dB, far past the clamp
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("very quiet"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Quiet Track")
	if err := svc.GenerateLoudness(context.Background(), media.ID); err != nil {
		t.Fatal(err)
	}
	analysis := readLoudness(t, svc, fingerprint)
	if analysis.GainDB != loudnessGainClampDB {
		t.Fatalf("gainDB = %v, want clamp %v", analysis.GainDB, loudnessGainClampDB)
	}
}

func readLoudness(t *testing.T, svc *Service, fingerprint string) LoudnessAnalysis {
	t.Helper()
	data, err := os.ReadFile(svc.LoudnessPath(fingerprint))
	if err != nil {
		t.Fatalf("loudness missing: %v", err)
	}
	var analysis LoudnessAnalysis
	if err := json.Unmarshal(data, &analysis); err != nil {
		t.Fatalf("loudness is not JSON: %v", err)
	}
	return analysis
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
