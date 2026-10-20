package contract

// Photosensitivity safety contract.
//
// The Visual Runtime spec (docs/architecture/visual-runtime.md, "Safety Contract
// manifest") requires every Visual to declare photosensitivity bounds: a
// flash-frequency ceiling, a luminance-delta ceiling, and a strobe ban. This file
// owns two enforcement seams that keep those declarations honest:
//
//  1. Manifest validation (ValidatePhotosensitivityManifest): rejects a Visual
//     manifest whose declared bounds are missing or looser than the platform
//     hard maxima. Run at pack load and in CI so an unsafe manifest never ships.
//  2. Runtime analysis + clamp (AnalyzeFlashSequence / ClampFlashSequence): a
//     PEAT-style flash analysis of an emitted frame sequence, and a clamp that
//     attenuates out-of-bounds frames rather than rendering them.
//
// The hard maxima below are the same ceilings the Director's baseline clamp
// composes (ComposeDirectorClamps): flash <= 3 Hz, luminance delta <= 0.8, flash
// area <= 0.25, and no sustained strobe. They are derived from the WCAG 2.x
// three-flash / general-flash thresholds that PEAT (Photosensitive Epilepsy
// Analysis Tool) enforces; a Visual may declare stricter bounds but never looser.

import (
	"errors"
	"fmt"
	"math"
)

// Platform hard maxima. A declared bound looser than these fails validation.
const (
	MaxDeclaredFlashHz        = 3.0
	MaxDeclaredLuminanceDelta = 0.8
	MaxDeclaredFlashArea      = 0.25
)

// PhotosensitivityBounds is a Visual's declared safety ceiling. StrobeBanned must
// be true (a sustained regular flash pattern is never permitted); the numeric
// ceilings must be positive and at or below the platform hard maxima.
type PhotosensitivityBounds struct {
	MaxFlashHz        float64 `json:"maxFlashHz"`
	MaxLuminanceDelta float64 `json:"maxLuminanceDelta"`
	MaxFlashArea      float64 `json:"maxFlashArea"`
	StrobeBanned      bool    `json:"strobeBanned"`
}

// ValidatePhotosensitivityManifest rejects a bounds declaration that is missing,
// non-positive, banned-flag-clear, or looser than the platform hard maximum on
// any axis. This is the pack-load / CI gate: an over-ceiling manifest fails build.
func ValidatePhotosensitivityManifest(b PhotosensitivityBounds) error {
	if !b.StrobeBanned {
		return errors.New("photosensitivity: strobe must be banned (strobeBanned=true)")
	}
	if err := validateCeiling("maxFlashHz", b.MaxFlashHz, MaxDeclaredFlashHz); err != nil {
		return err
	}
	if err := validateCeiling("maxLuminanceDelta", b.MaxLuminanceDelta, MaxDeclaredLuminanceDelta); err != nil {
		return err
	}
	if err := validateCeiling("maxFlashArea", b.MaxFlashArea, MaxDeclaredFlashArea); err != nil {
		return err
	}
	return nil
}

func validateCeiling(name string, value, hardMax float64) error {
	if value <= 0 {
		return fmt.Errorf("photosensitivity: %s must be positive, got %g", name, value)
	}
	if value > hardMax {
		return fmt.Errorf("photosensitivity: %s=%g exceeds hard maximum %g", name, value, hardMax)
	}
	return nil
}

// DirectorClamps derived from validated bounds, for feeding the runtime Director
// as an active safe-mode clamp.
func (b PhotosensitivityBounds) DirectorClamps() DirectorClamps {
	return DirectorClamps{
		Name:              "photosensitive-safe",
		MaxFlashHz:        b.MaxFlashHz,
		MaxLuminanceDelta: b.MaxLuminanceDelta,
		MaxFlashArea:      b.MaxFlashArea,
	}
}

// FlashAnalysis is the PEAT-style result of scanning a frame sequence: the peak
// flash rate observed in any sliding 1-second window, the peak per-frame
// luminance delta, and whether a sustained regular strobe pattern was detected.
type FlashAnalysis struct {
	PeakFlashHz        float64 `json:"peakFlashHz"`
	PeakLuminanceDelta float64 `json:"peakLuminanceDelta"`
	StrobeDetected     bool    `json:"strobeDetected"`
}

// Exceeds reports whether the analysis violates the given bounds on any axis.
func (a FlashAnalysis) Exceeds(b PhotosensitivityBounds) bool {
	if a.PeakFlashHz > b.MaxFlashHz {
		return true
	}
	if a.PeakLuminanceDelta > b.MaxLuminanceDelta {
		return true
	}
	if b.StrobeBanned && a.StrobeDetected {
		return true
	}
	return false
}

// flashThreshold is the onset magnitude at or above which a frame counts as a
// flash event, matching the Director's runtime flash-guard trip point.
const flashThreshold = 0.80

// AnalyzeFlashSequence performs a PEAT-style flash analysis over an emitted frame
// sequence. A frame is a flash when its broadband onset (the luminance-driving
// signal for beat-reactive Visuals) is at or above flashThreshold. The peak flash
// rate is the maximum count of flash events in any 1-second sliding window; the
// peak luminance delta is the largest single-frame broadband onset; a strobe is
// detected when three or more evenly-spaced flashes occur within one second.
func AnalyzeFlashSequence(frames []AudioFrame) FlashAnalysis {
	flashes := flashTimestamps(frames)
	return FlashAnalysis{
		PeakFlashHz:        peakWindowRate(flashes),
		PeakLuminanceDelta: peakLuminanceDelta(frames),
		StrobeDetected:     strobeDetected(flashes),
	}
}

func flashTimestamps(frames []AudioFrame) []float64 {
	out := make([]float64, 0, len(frames))
	for _, f := range frames {
		if f.Onset.Broadband >= flashThreshold {
			out = append(out, f.MediaTimeMS)
		}
	}
	return out
}

func peakLuminanceDelta(frames []AudioFrame) float64 {
	peak := 0.0
	for _, f := range frames {
		peak = math.Max(peak, f.Onset.Broadband)
	}
	return round6(peak)
}

// peakWindowRate returns the largest number of flash events falling inside any
// 1000 ms window anchored at a flash timestamp.
func peakWindowRate(flashes []float64) float64 {
	peak := 0.0
	for i, start := range flashes {
		count := 0.0
		for _, t := range flashes[i:] {
			if t-start >= 1000 {
				break
			}
			count++
		}
		peak = math.Max(peak, count)
	}
	return peak
}

// strobeDetected reports a sustained regular strobe: three or more flashes inside
// a 1-second window with near-uniform spacing (the WCAG general-flash pattern).
func strobeDetected(flashes []float64) bool {
	for i := 0; i+2 < len(flashes); i++ {
		a, b, c := flashes[i], flashes[i+1], flashes[i+2]
		if c-a >= 1000 {
			continue
		}
		gap1, gap2 := b-a, c-b
		if regularGaps(gap1, gap2) {
			return true
		}
	}
	return false
}

func regularGaps(gap1, gap2 float64) bool {
	if gap1 <= 0 || gap2 <= 0 {
		return false
	}
	return math.Abs(gap1-gap2) <= 0.25*math.Max(gap1, gap2)
}

// ClampFlashSequence is the runtime clamp: it returns an attenuated copy of the
// frame sequence whose flash analysis is guaranteed to satisfy the bounds. Rather
// than dropping frames, over-ceiling flash frames have their broadband onset
// attenuated to the declared luminance-delta ceiling, and once the declared
// flash-rate ceiling is reached within a sliding 1-second window, further flash
// frames in that window are attenuated below the flash threshold. The input slice
// is not mutated.
func ClampFlashSequence(frames []AudioFrame, b PhotosensitivityBounds) []AudioFrame {
	out := make([]AudioFrame, len(frames))
	copy(out, frames)
	var window []float64
	for i := range out {
		attenuateLuminance(&out[i], b)
		if out[i].Onset.Broadband < flashThreshold {
			continue
		}
		window = trimWindow(window, out[i].MediaTimeMS)
		if float64(len(window)) >= b.MaxFlashHz || (b.StrobeBanned && completesStrobe(window, out[i].MediaTimeMS)) {
			attenuateFlash(&out[i])
			continue
		}
		window = append(window, out[i].MediaTimeMS)
	}
	return out
}

func attenuateLuminance(f *AudioFrame, b PhotosensitivityBounds) {
	if f.Onset.Broadband > b.MaxLuminanceDelta {
		f.Onset.Broadband = round6(b.MaxLuminanceDelta)
	}
	if f.Onset.Low > b.MaxLuminanceDelta {
		f.Onset.Low = round6(b.MaxLuminanceDelta)
	}
	if f.Onset.Mid > b.MaxLuminanceDelta {
		f.Onset.Mid = round6(b.MaxLuminanceDelta)
	}
	if f.Onset.High > b.MaxLuminanceDelta {
		f.Onset.High = round6(b.MaxLuminanceDelta)
	}
}

func attenuateFlash(f *AudioFrame) {
	v := round6(flashThreshold * 0.5)
	f.Onset.Broadband = v
	if f.Onset.Low > v {
		f.Onset.Low = v
	}
	if f.Onset.Mid > v {
		f.Onset.Mid = v
	}
	if f.Onset.High > v {
		f.Onset.High = v
	}
}

func completesStrobe(window []float64, now float64) bool {
	if len(window) < 2 {
		return false
	}
	flashes := append(append([]float64(nil), window...), now)
	return strobeDetected(flashes)
}

func trimWindow(window []float64, now float64) []float64 {
	kept := window[:0]
	for _, t := range window {
		if now-t < 1000 {
			kept = append(kept, t)
		}
	}
	return kept
}
