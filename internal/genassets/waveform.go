package genassets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"

	"github.com/beamfall/core/internal/store"
)

// Waveform-envelope spec (ADR-0035 §6, amends ADR-0033 §2): amplitude-over-time
// sampled at ~1000 evenly-spaced points across the full duration, normalised to
// [0,1]. Consumers are Ambient Display's precomputed visualiser and the Music
// Plugin player's scrub-bar waveform — both render an envelope, so
// the point count is a fixed server tunable, not a client commitment.
const (
	// WaveformPoints is the target envelope resolution. FFmpeg reports one
	// amplitude per analysis window; bucketWaveform folds those windows down to
	// exactly this many points (or fewer, when the source has fewer windows than
	// points — a very short track degrades gracefully rather than padding).
	WaveformPoints = 1000
	// waveformVersion is the manifest schema version clients branch on.
	waveformVersion = 1
)

// WaveformName is the wire vocabulary asset name for the waveform envelope —
// the closed validAssetName set and the .json content-type branch in the
// serving route both already cover it.
const WaveformName = "waveform.json"

// WaveformEnvelope is the JSON document clients consume: the normalised [0,1]
// amplitude points and their count. The envelope is dimensionless time — points
// are evenly spaced across the full duration, so the client maps point index to
// playhead by (index/len) without a per-point timestamp.
type WaveformEnvelope struct {
	Version int       `json:"version"`
	Points  []float64 `json:"points"`
}

// WaveformPath returns where a fingerprint's waveform envelope lives (whether or
// not it exists yet).
func (s *Service) WaveformPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), WaveformName)
}

// WaveformAsset reports the waveform envelope name when one exists for the
// fingerprint (the Music player / Ambient availability seam).
func (s *Service) WaveformAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.WaveformPath(fingerprint)) {
		return "", false
	}
	return WaveformName, true
}

// audioSource loads the Media row and primary file when the item is audio-class
// with a content fingerprint; ok=false means "nothing to generate", which is
// not an error. The audio-class gate is the waveform's analogue of videoSource —
// the waveform is an audio property, so video-class Media never gets one (v1 is
// audio-only, ADR-0035 §6).
func (s *Service) audioSource(ctx context.Context, id store.MediaID) (store.Media, store.MediaFile, bool, error) {
	return s.selectSource(ctx, id, func(m store.Media) bool {
		return m.Class == store.MediaClassAudio
	})
}

// GenerateWaveform produces the waveform envelope for one audio-class Media
// item's primary file. An existing envelope for the same fingerprint means
// nothing to do — the file is keyed by content fingerprint exactly like every
// other Generated Asset, so a quality-upgrade replacement (new bytes → new
// fingerprint → new directory) regenerates while a metadata edit (same bytes,
// same fingerprint, same directory) never does. The envelope is written last,
// atomically — it is the publication marker.
func (s *Service) GenerateWaveform(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	_, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	dst := s.WaveformPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	samples, err := s.gen.WaveformAmplitudes(ctx, file.Path)
	if err != nil {
		return err
	}
	points := normalizeWaveform(bucketWaveform(samples, WaveformPoints))
	if len(points) == 0 {
		return fmt.Errorf("waveform analysis for %s produced no amplitude samples", id)
	}
	data, err := json.Marshal(WaveformEnvelope{Version: waveformVersion, Points: points})
	if err != nil {
		return err
	}
	return s.publishBytes(string(id), file.ContentFingerprint, "waveform:v1", WaveformName, data)
}

// bucketWaveform folds per-window amplitude samples down to at most target
// evenly-spaced points by taking the peak of each contiguous bucket — peak, not
// mean, so a transient (the visible feature of a waveform) survives downsampling
// rather than being averaged away. Fewer samples than the target collapses to
// one point per sample (no upsampling/padding); zero samples returns nil.
func bucketWaveform(samples []float64, target int) []float64 {
	if len(samples) == 0 || target <= 0 {
		return nil
	}
	if len(samples) <= target {
		out := make([]float64, len(samples))
		copy(out, samples)
		return out
	}
	out := make([]float64, target)
	for i := range out {
		lo := i * len(samples) / target
		hi := (i + 1) * len(samples) / target
		if hi <= lo {
			hi = lo + 1
		}
		peak := samples[lo]
		for _, v := range samples[lo:hi] {
			if v > peak {
				peak = v
			}
		}
		out[i] = peak
	}
	return out
}

// normalizeWaveform scales amplitudes into [0,1] against the loudest point —
// the [0,1] commitment of ADR-0035 §6. A silent or single-valued envelope (max
// <= 0) clamps to all-zeros rather than dividing by zero. Negative inputs (none
// expected from a magnitude measure) clamp to 0.
func normalizeWaveform(samples []float64) []float64 {
	max := 0.0
	for _, v := range samples {
		if v > max {
			max = v
		}
	}
	out := make([]float64, len(samples))
	if max <= 0 {
		return out
	}
	for i, v := range samples {
		n := v / max
		if n < 0 {
			n = 0
		} else if n > 1 {
			n = 1
		}
		out[i] = math.Round(n*1000) / 1000
	}
	return out
}
