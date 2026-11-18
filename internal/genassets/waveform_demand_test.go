package genassets

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestEnsureWaveformGeneratesMissingEnvelopeOnce(t *testing.T) {
	// feature:waveform feature:generated-assets
	gen := &fakeGen{waveformSamples: []float64{0.2, 0.8, 0.4}}
	svc, st, _ := newTestService(t, gen)
	track := filepath.Join(t.TempDir(), "track.bin")
	if err := os.WriteFile(track, []byte("audio demand"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, fingerprint := seedAudioMedia(t, st, track, "Demand Track")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.EnsureWaveform(context.Background(), media.ID, fingerprint)
		}()
	}
	wg.Wait()
	if _, err := svc.AssetPath(fingerprint, WaveformName); err != nil {
		t.Fatalf("waveform not generated on demand: %v", err)
	}
	if gen.waveformCalls != 1 {
		t.Fatalf("waveform generations = %d, want 1 shared by concurrent requests", gen.waveformCalls)
	}
	svc.EnsureWaveform(context.Background(), media.ID, fingerprint)
	if gen.waveformCalls != 1 {
		t.Fatalf("an existing waveform was regenerated: %d calls", gen.waveformCalls)
	}
}

func TestEnsureWaveformLeavesVideoWithoutEnvelope(t *testing.T) {
	// feature:waveform feature:generated-assets
	gen := &fakeGen{waveformSamples: []float64{0.5}}
	svc, _, fingerprint := newTestService(t, gen)
	svc.EnsureWaveform(context.Background(), "media-alpha", fingerprint)
	if _, ok := svc.WaveformAsset(fingerprint); ok {
		t.Fatal("video Media got a waveform on demand")
	}
	if gen.waveformCalls != 0 {
		t.Fatalf("waveform generations = %d, want 0 for video", gen.waveformCalls)
	}
}
