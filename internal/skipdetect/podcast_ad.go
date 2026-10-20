package skipdetect

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

const (
	audioAdWeightCount      = 5
	audioAdClassThreshold   = 0.5
	audioAdTimestampEpsilon = 1e-4
	// audioAdRMSDBFSFloor mirrors ffmpeg's silence sentinel. RMS in dBFS is
	// bounded above by full scale (0) and below by the extraction boundary's
	// silence floor; anything outside that is not a plausible measurement.
	audioAdRMSDBFSFloor = -120.0
	// audioAdSpectralNyquistHz mirrors ffmpeg's audioAdSampleRate/2 (the
	// feature-set's 8192 Hz analysis rate, halved). Spectral centroid and
	// rolloff above this Nyquist ceiling cannot be produced by the extractor
	// and indicate a malformed or out-of-range frame.
	audioAdSpectralNyquistHz = 4096.0
)

// AudioAdWeights is the fixed [bias, RMS, centroid, flatness, rolloff] model
// layout. It is numeric data only; no model-supplied code is executable.
type AudioAdWeights [audioAdWeightCount]float64

// AudioAdModel is one flavor-specific, weights-only classifier artifact.
type AudioAdModel struct {
	ModelID              string
	ModelVersion         string
	FeatureSetVersion    string
	Flavor               store.SegmentFlavor
	ClassWeights         AudioAdWeights
	StartBoundaryWeights AudioAdWeights
	EndBoundaryWeights   AudioAdWeights
}

// AudioAdCandidate keeps class and boundary evidence separate.
type AudioAdCandidate struct {
	ModelID           string
	ModelVersion      string
	FeatureSetVersion string
	Flavor            store.SegmentFlavor
	StartSeconds      float64
	EndSeconds        float64
	ClassConfidence   float64
	BoundaryScore     float64
}

// AudioAdCalibration is release-time holdout evidence. Marker admission uses
// only boundary precision; class F1 remains evaluation evidence.
type AudioAdCalibration struct {
	ModelID                  string
	ModelVersion             string
	FeatureSetVersion        string
	Flavor                   store.SegmentFlavor
	HoldoutBoundaryPrecision float64
	BoundaryPrecisionFloor   float64
	HoldoutClassF1           float64
}

func (m AudioAdModel) Validate() error {
	if strings.TrimSpace(m.ModelID) == "" {
		return errors.New("audio ad model ID is required")
	}
	if strings.TrimSpace(m.ModelVersion) == "" {
		return errors.New("audio ad model version is required")
	}
	if m.FeatureSetVersion != ffmpeg.AudioAdFeatureSetVersion {
		return errors.New("audio ad model feature-set version is incompatible")
	}
	if !audioAdFlavor(m.Flavor) {
		return fmt.Errorf("unknown audio ad flavor %q", m.Flavor)
	}
	for _, weights := range [...]AudioAdWeights{
		m.ClassWeights,
		m.StartBoundaryWeights,
		m.EndBoundaryWeights,
	} {
		for _, weight := range weights {
			if !finiteAudioAd(weight) {
				return errors.New("audio ad model contains a non-finite weight")
			}
		}
	}
	return nil
}

// Infer returns bounded candidates from contiguous class-positive runs. The
// boundary heads choose the final edges and never decide marker admission.
func (m AudioAdModel) Infer(features ffmpeg.AudioAdFeatureSet) ([]AudioAdCandidate, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if err := validateAudioAdFeatures(features); err != nil {
		return nil, err
	}
	scores := make([]audioAdFrameScores, len(features.Frames))
	for i, frame := range features.Frames {
		var err error
		scores[i].class, err = scoreAudioAd(m.ClassWeights, frame)
		if err != nil {
			return nil, err
		}
		scores[i].start, err = scoreAudioAd(m.StartBoundaryWeights, frame)
		if err != nil {
			return nil, err
		}
		scores[i].end, err = scoreAudioAd(m.EndBoundaryWeights, frame)
		if err != nil {
			return nil, err
		}
	}
	var candidates []AudioAdCandidate
	for i := 0; i < len(scores); {
		if scores[i].class < audioAdClassThreshold {
			i++
			continue
		}
		runStart := i
		for i < len(scores) && scores[i].class >= audioAdClassThreshold {
			i++
		}
		runEnd := i - 1
		start := maxAudioAdScore(scores, runStart, runEnd, func(score audioAdFrameScores) float64 {
			return score.start
		})
		end := maxAudioAdScore(scores, start, runEnd, func(score audioAdFrameScores) float64 {
			return score.end
		})
		var classTotal float64
		for j := start; j <= end; j++ {
			classTotal += scores[j].class
		}
		candidate := AudioAdCandidate{
			ModelID:           m.ModelID,
			ModelVersion:      m.ModelVersion,
			FeatureSetVersion: m.FeatureSetVersion,
			Flavor:            m.Flavor,
			StartSeconds:      features.Frames[start].StartSeconds,
			EndSeconds:        features.Frames[end].StartSeconds + features.WindowSeconds,
			ClassConfidence:   classTotal / float64(end-start+1),
			BoundaryScore:     math.Min(scores[start].start, scores[end].end),
		}
		if err := candidate.validate(); err != nil {
			return nil, err
		}
		if candidate.StartSeconds < features.Frames[0].StartSeconds ||
			candidate.EndSeconds > features.Frames[len(features.Frames)-1].StartSeconds+features.WindowSeconds {
			return nil, errors.New("audio ad candidate exceeds feature bounds")
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

type audioAdFrameScores struct {
	class float64
	start float64
	end   float64
}

func scoreAudioAd(weights AudioAdWeights, frame ffmpeg.AudioAdFeatureFrame) (float64, error) {
	vector := AudioAdWeights{
		1,
		frame.RMSDBFS,
		frame.SpectralCentroidHz,
		frame.SpectralFlatness,
		frame.SpectralRolloffHz,
	}
	var sum float64
	for i := range weights {
		sum += weights[i] * vector[i]
	}
	if !finiteAudioAd(sum) {
		return 0, errors.New("audio ad model produced a non-finite score")
	}
	if sum >= 0 {
		exp := math.Exp(-sum)
		return 1 / (1 + exp), nil
	}
	exp := math.Exp(sum)
	return exp / (1 + exp), nil
}

func maxAudioAdScore(
	scores []audioAdFrameScores,
	start, end int,
	value func(audioAdFrameScores) float64,
) int {
	best := start
	for i := start + 1; i <= end; i++ {
		if value(scores[i]) > value(scores[best]) {
			best = i
		}
	}
	return best
}

func validateAudioAdFeatures(features ffmpeg.AudioAdFeatureSet) error {
	if features.Version != ffmpeg.AudioAdFeatureSetVersion {
		return errors.New("audio ad feature-set version is incompatible")
	}
	if !finiteAudioAd(features.WindowSeconds) || math.Abs(features.WindowSeconds-ffmpeg.AudioAdFeatureWindowSeconds) > audioAdTimestampEpsilon {
		return errors.New("audio ad feature window is incompatible")
	}
	if len(features.Frames) == 0 {
		return errors.New("audio ad feature set has no frames")
	}
	for i, frame := range features.Frames {
		values := [...]float64{
			frame.StartSeconds,
			frame.RMSDBFS,
			frame.SpectralCentroidHz,
			frame.SpectralFlatness,
			frame.SpectralRolloffHz,
		}
		for _, value := range values {
			if !finiteAudioAd(value) {
				return fmt.Errorf("audio ad feature frame %d contains a non-finite value", i)
			}
		}
		if frame.StartSeconds < 0 ||
			frame.RMSDBFS < audioAdRMSDBFSFloor || frame.RMSDBFS > 0 ||
			frame.SpectralCentroidHz < 0 || frame.SpectralCentroidHz > audioAdSpectralNyquistHz ||
			frame.SpectralFlatness < 0 || frame.SpectralFlatness > 1 ||
			frame.SpectralRolloffHz < 0 || frame.SpectralRolloffHz > audioAdSpectralNyquistHz {
			return fmt.Errorf("audio ad feature frame %d is out of range", i)
		}
		if i > 0 && math.Abs(frame.StartSeconds-features.Frames[i-1].StartSeconds-features.WindowSeconds) > audioAdTimestampEpsilon {
			return fmt.Errorf("audio ad feature frame %d is not contiguous", i)
		}
	}
	return nil
}

func (c AudioAdCandidate) validate() error {
	if strings.TrimSpace(c.ModelID) == "" || strings.TrimSpace(c.ModelVersion) == "" {
		return errors.New("audio ad candidate model identity is required")
	}
	if c.FeatureSetVersion != ffmpeg.AudioAdFeatureSetVersion {
		return errors.New("audio ad candidate feature-set version is incompatible")
	}
	if !audioAdFlavor(c.Flavor) {
		return fmt.Errorf("unknown audio ad flavor %q", c.Flavor)
	}
	if !finiteAudioAd(c.StartSeconds) || !finiteAudioAd(c.EndSeconds) ||
		c.StartSeconds < 0 || c.EndSeconds <= c.StartSeconds {
		return errors.New("audio ad candidate range is invalid")
	}
	if !audioAdProbability(c.ClassConfidence) || !audioAdProbability(c.BoundaryScore) {
		return errors.New("audio ad candidate score is invalid")
	}
	return nil
}

func (c AudioAdCalibration) Validate() error {
	if strings.TrimSpace(c.ModelID) == "" || strings.TrimSpace(c.ModelVersion) == "" {
		return errors.New("audio ad calibration model identity is required")
	}
	if c.FeatureSetVersion != ffmpeg.AudioAdFeatureSetVersion {
		return errors.New("audio ad calibration feature-set version is incompatible")
	}
	if !audioAdFlavor(c.Flavor) {
		return fmt.Errorf("unknown audio ad flavor %q", c.Flavor)
	}
	for _, value := range [...]float64{
		c.HoldoutBoundaryPrecision,
		c.BoundaryPrecisionFloor,
		c.HoldoutClassF1,
	} {
		if !audioAdProbability(value) {
			return errors.New("audio ad calibration contains an invalid value")
		}
	}
	return nil
}

// admits is a package-local release gate, not a per-marker or wire property.
// Class F1 and candidate scores never affect admission; host-read stays hidden.
func (c AudioAdCalibration) admits(candidate AudioAdCandidate) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	if err := candidate.validate(); err != nil {
		return false, err
	}
	if c.ModelID != candidate.ModelID || c.ModelVersion != candidate.ModelVersion ||
		c.FeatureSetVersion != candidate.FeatureSetVersion || c.Flavor != candidate.Flavor {
		return false, errors.New("audio ad calibration does not match candidate")
	}
	if c.Flavor == store.FlavorHostRead {
		return false, nil
	}
	return c.HoldoutBoundaryPrecision >= c.BoundaryPrecisionFloor, nil
}

func audioAdFlavor(flavor store.SegmentFlavor) bool {
	return flavor == store.FlavorInserted || flavor == store.FlavorHostRead
}

func audioAdProbability(value float64) bool {
	return finiteAudioAd(value) && value >= 0 && value <= 1
}

func finiteAudioAd(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
