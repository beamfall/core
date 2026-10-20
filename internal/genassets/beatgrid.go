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

// Beat-grid spec: BPM estimate + per-beat timestamps
// (seconds) + a confidence in [0,1], derived from audio analysis. It is the
// optional, enrichment-produced sibling of the waveform envelope — clients
// degrade gracefully when it is absent (tempo-synced Visuals fall back to
// real-time), so its existence is never a hard gate. Like every Generated Asset
// it is content-fingerprint-keyed and served behind Stream Tokens.
//
// *** Generation tooling is a dependency-light pure-Go DSP STAND-IN, not the
// *** production beat tracker. ADR-0035 §6 left the tooling TBD and the 2026-06-13
// *** visuals spike recommends Essentia RhythmExtractor2013 in the Enrichment
// *** Plugin — a heavy native (cgo) dependency that is NOT present
// *** in the build/gate environment, and madmom is rejected (non-commercial
// *** model license). This stand-in estimates tempo from the per-window energy
// *** envelope FFmpeg already produces for the waveform (WaveformAmplitudes): an
// *** onset-strength lane → autocorrelation over the plausible BPM band → a
// *** constant-tempo beat phase. It is deterministic and good enough to exercise
// *** the Generated Asset contract, the serving path, and beat-synced client
// *** fallback end-to-end, offline and without vendoring a DSP library. A future
// *** Essentia/aubio backend swaps in behind the BeatAnalyzer seam below with no
// *** Core contract change (same asset name, same JSON shape, same regeneration
// *** semantics). TODO: swap the stand-in analyzer for
// *** the Essentia RhythmExtractor2013 backend when the Enrichment Plugin DSP
// *** lands; bump beatGridVersion only if the wire shape changes.
const beatGridVersion = 1

// Tempo band the stand-in searches. A real tracker resolves octave errors and
// rubato; the stand-in clamps to a single plausible range so half/double-tempo
// aliasing is bounded, and folds an out-of-band candidate back in.
const (
	minBPM = 60.0
	maxBPM = 180.0
)

// BeatGridName is the wire vocabulary asset name for the beat grid — added to the
// closed validAssetName set and served by the .json content-type branch in the
// media route exactly like the waveform envelope and silence map.
const BeatGridName = "beatgrid.json"

// BeatGrid is the JSON document clients consume: the estimated tempo, the
// per-beat timestamps in seconds from the start of the stream, and a confidence
// in [0,1] (how peaked the tempo autocorrelation was). A track whose energy
// envelope yields no usable tempo publishes an empty beat list with zero
// confidence — the asset's existence is the "analysed" marker, distinct from
// "not yet analysed", so a client never blocks waiting on a grid that will never
// improve. Beats are evenly spaced (constant tempo) in the stand-in; the JSON
// shape already carries per-beat timestamps so a variable-tempo backend can
// emit irregular spacing without a wire change.
type BeatGrid struct {
	Version    int       `json:"version"`
	BPM        float64   `json:"bpm"`
	Confidence float64   `json:"confidence"`
	Beats      []float64 `json:"beats"`
}

// BeatGridPath returns where a fingerprint's beat grid lives (whether or not it
// exists yet).
func (s *Service) BeatGridPath(fingerprint string) string {
	return filepath.Join(s.assetDir(fingerprint), BeatGridName)
}

// BeatGridAsset reports the beat-grid name when one exists for the fingerprint
// (the tempo-synced Visuals availability seam — optional, ADR-0047 §3).
func (s *Service) BeatGridAsset(fingerprint string) (string, bool) {
	if fingerprint == "" || !regularFileExists(s.BeatGridPath(fingerprint)) {
		return "", false
	}
	return BeatGridName, true
}

// GenerateBeatGrid produces the beat grid for one audio-class Media item's
// primary file. Like every other Generated Asset it is keyed by content
// fingerprint: a quality-upgrade replacement (new bytes → new fingerprint → new
// directory) regenerates, while a metadata edit (same bytes, same fingerprint,
// same directory) never does. The grid is written last, atomically — it is the
// publication marker, so a present file with an empty beat list means "analysed,
// no usable tempo", not "not yet analysed".
//
// The stand-in reuses the per-window energy envelope FFmpeg already computes for
// the waveform (WaveformAmplitudes) rather than adding a second decode pass — the
// windows span the full duration, so window index maps linearly to seconds via
// the Media's runtime.
func (s *Service) GenerateBeatGrid(ctx context.Context, id store.MediaID) error {
	if s.gen == nil {
		return errors.New("no generator configured")
	}
	media, file, ok, err := s.audioSource(ctx, id)
	if err != nil || !ok {
		return err
	}
	if media.DurationSeconds <= 0 {
		// No runtime → window index cannot be mapped to seconds. Nothing to
		// generate; not an error (mirrors the videoSource runtime gate).
		return nil
	}
	dst := s.BeatGridPath(file.ContentFingerprint)
	if regularFileExists(dst) {
		return nil
	}
	envelope, err := s.gen.WaveformAmplitudes(ctx, file.Path)
	if err != nil {
		return err
	}
	grid := estimateBeatGrid(envelope, float64(media.DurationSeconds))
	data, err := json.Marshal(grid)
	if err != nil {
		return err
	}
	if err := s.publishBytes(string(id), file.ContentFingerprint, "beat-grid:v1", BeatGridName, data); err != nil {
		return fmt.Errorf("publishing beat grid for %s: %w", id, err)
	}
	return nil
}

// estimateBeatGrid is the pure-Go stand-in DSP. Given the per-window energy
// envelope (one non-negative magnitude per analysis window, spanning the full
// duration) and the duration in seconds, it estimates a constant tempo and lays
// out evenly-spaced beats:
//
//  1. Onset-strength lane: positive first-difference of the envelope (energy
//     rising = a note/transient onset; falling = decay, ignored). This is the
//     "onset-strength lane" of ADR-0035 §6.
//  2. Tempo: autocorrelate the onset lane over the lag range that maps to
//     [minBPM, maxBPM] and pick the strongest periodicity. Confidence is the
//     peak's prominence above the mean autocorrelation, clamped to [0,1].
//  3. Phase: align the first beat to the strongest onset within the first beat
//     period, then step at the tempo period to the end.
//
// A too-short or energy-flat envelope yields an empty grid with zero confidence
// (no false tempo). Deterministic: identical input → identical grid.
func estimateBeatGrid(envelope []float64, durationSeconds float64) BeatGrid {
	grid := BeatGrid{Version: beatGridVersion, Beats: []float64{}}
	n := len(envelope)
	// Need at least a couple of beats' worth of windows at the slowest tempo to
	// say anything. secondsPerWindow is the linear window→time map.
	if n < 8 || durationSeconds <= 0 {
		return grid
	}
	secondsPerWindow := durationSeconds / float64(n)

	onset := onsetStrength(envelope)

	// Lag bounds (in windows) for the BPM band. A faster tempo = shorter period =
	// smaller lag. Guard against a degenerate window resolution where the whole
	// band collapses to fewer than two distinct lags.
	minLag := int(math.Floor(60.0 / maxBPM / secondsPerWindow))
	maxLag := int(math.Ceil(60.0 / minBPM / secondsPerWindow))
	if minLag < 1 {
		minLag = 1
	}
	if maxLag >= n {
		maxLag = n - 1
	}
	if maxLag <= minLag {
		return grid
	}

	bestLag, confidence := dominantPeriod(onset, minLag, maxLag)
	if bestLag <= 0 || confidence <= 0 {
		return grid
	}

	periodSeconds := float64(bestLag) * secondsPerWindow
	bpm := 60.0 / periodSeconds
	// Fold an octave-aliased estimate back into the band (the autocorrelation can
	// lock onto a half/double-time period at the band edge).
	for bpm < minBPM {
		bpm *= 2
	}
	for bpm > maxBPM {
		bpm /= 2
	}
	grid.BPM = math.Round(bpm*10) / 10
	grid.Confidence = math.Round(confidence*1000) / 1000

	// Phase: strongest onset within the first period anchors beat zero.
	phaseWindow := 0
	phasePeak := onset[0]
	for i := 1; i < bestLag && i < len(onset); i++ {
		if onset[i] > phasePeak {
			phasePeak = onset[i]
			phaseWindow = i
		}
	}
	start := float64(phaseWindow) * secondsPerWindow
	for t := start; t < durationSeconds; t += periodSeconds {
		grid.Beats = append(grid.Beats, math.Round(t*1000)/1000)
	}
	return grid
}

// onsetStrength is the half-wave-rectified first difference of the energy
// envelope: positive energy change (an onset) is kept, decay is zeroed. This is
// the onset-strength lane the tempo estimate runs on. The result has the same
// length as the input (the first sample has no predecessor → 0).
func onsetStrength(envelope []float64) []float64 {
	out := make([]float64, len(envelope))
	for i := 1; i < len(envelope); i++ {
		if d := envelope[i] - envelope[i-1]; d > 0 {
			out[i] = d
		}
	}
	return out
}

// dominantPeriod autocorrelates the onset lane over [minLag, maxLag] and returns
// the lag with the strongest periodicity plus a confidence in [0,1] measuring how
// far that peak stands above the mean autocorrelation across the band (1 = a lone
// sharp peak, 0 = flat / no periodicity). A flat or zero-energy lane returns
// (0, 0): no tempo.
func dominantPeriod(onset []float64, minLag, maxLag int) (bestLag int, confidence float64) {
	n := len(onset)
	var bestCorr, sumCorr float64
	count := 0
	for lag := minLag; lag <= maxLag; lag++ {
		var corr float64
		for i := lag; i < n; i++ {
			corr += onset[i] * onset[i-lag]
		}
		sumCorr += corr
		count++
		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}
	if bestCorr <= 0 || count == 0 {
		return 0, 0
	}
	mean := sumCorr / float64(count)
	// Prominence of the peak over the band mean, normalised by the peak so it is
	// scale-free (loud and quiet tracks of the same regularity score alike).
	confidence = (bestCorr - mean) / bestCorr
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
	}
	return bestLag, confidence
}
