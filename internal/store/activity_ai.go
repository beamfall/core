package store

import (
	"errors"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	PodcastAdDetectorMethod   = "podcast-ad-audio"
	DetectionOutcomeCompleted = "completed"
)

var (
	ErrDetectionRunAlreadyCompleted = errors.New("detection run identity is already completed")
	ErrDetectionRunStale            = errors.New("detection run completed before the active run")
	ErrDetectionModelAlreadyRevoked = errors.New("detection model identity is already revoked")
	ErrDetectionLifecycleNotFound   = errors.New("detection lifecycle event not found")
)

// DetectionIdentity is the complete inference identity. Eligibility must key on
// every field so either a model, artifact, feature set, or calibration change
// reopens a Media item.
type DetectionIdentity struct {
	ModelID            string
	ModelVersion       string
	ModelHash          string
	ArtifactIdentity   string
	FeatureSetVersion  string
	CalibrationID      string
	CalibrationVersion string
	CalibrationHash    string
}

// DetectionModelIdentity is the model-version scope used for registry
// revocation. Feature/calibration changes do not broaden a revocation.
type DetectionModelIdentity struct {
	ModelID      string
	ModelVersion string
	ModelHash    string
}

// DetectionCandidate separates class confidence from boundary admission.
// BoundaryEligible is decided by validated release calibration; the store
// records that decision and applies it without consulting ClassConfidence.
type DetectionCandidate struct {
	SegmentKind      string
	StartSeconds     float64
	EndSeconds       float64
	Flavor           SegmentFlavor
	ClassConfidence  float64
	BoundaryScore    float64
	BoundaryEligible bool
}

// DetectionRun is one completed immutable receipt and its candidate evidence.
// An empty Candidates slice is a successful zero-candidate receipt.
type DetectionRun struct {
	RunID          string
	MediaID        MediaID
	DetectorMethod string
	Identity       DetectionIdentity
	SignalTier     string
	Candidates     []DetectionCandidate
	CompletedAt    time.Time
}

func validateDetectionRun(run *DetectionRun) error {
	run.RunID = strings.TrimSpace(run.RunID)
	run.DetectorMethod = strings.TrimSpace(run.DetectorMethod)
	run.SignalTier = strings.TrimSpace(run.SignalTier)
	if run.RunID == "" {
		return ValidationError("detection run ID is required")
	}
	if run.MediaID == "" {
		return ValidationError("media ID is required")
	}
	if run.DetectorMethod == "" {
		return ValidationError("detector method is required")
	}
	if err := validateDetectionIdentity(&run.Identity); err != nil {
		return err
	}
	if run.SignalTier == "" {
		return ValidationError("signal tier is required")
	}
	if run.CompletedAt.IsZero() {
		return ValidationError("completion time is required")
	}
	run.CompletedAt = run.CompletedAt.UTC()
	for i := range run.Candidates {
		if err := validateDetectionCandidate(&run.Candidates[i]); err != nil {
			return err
		}
	}
	return nil
}

func validateDetectionIdentity(identity *DetectionIdentity) error {
	fields := []struct {
		name  string
		value *string
	}{
		{"model ID", &identity.ModelID},
		{"model version", &identity.ModelVersion},
		{"model hash", &identity.ModelHash},
		{"artifact identity", &identity.ArtifactIdentity},
		{"feature-set version", &identity.FeatureSetVersion},
		{"calibration ID", &identity.CalibrationID},
		{"calibration version", &identity.CalibrationVersion},
		{"calibration hash", &identity.CalibrationHash},
	}
	for _, field := range fields {
		*field.value = strings.TrimSpace(*field.value)
		if *field.value == "" {
			return ValidationError(field.name + " is required")
		}
	}
	return nil
}

func validateDetectionModelIdentity(model *DetectionModelIdentity) error {
	model.ModelID = strings.TrimSpace(model.ModelID)
	model.ModelVersion = strings.TrimSpace(model.ModelVersion)
	model.ModelHash = strings.TrimSpace(model.ModelHash)
	if model.ModelID == "" {
		return ValidationError("model ID is required")
	}
	if model.ModelVersion == "" {
		return ValidationError("model version is required")
	}
	if model.ModelHash == "" {
		return ValidationError("model hash is required")
	}
	return nil
}

func validateDetectionCandidate(candidate *DetectionCandidate) error {
	if !slices.Contains(SegmentKinds, candidate.SegmentKind) {
		return ValidationError("unknown segment kind " + candidate.SegmentKind)
	}
	if candidate.StartSeconds < 0 || !finite(candidate.StartSeconds) {
		return ValidationError("segment start must be finite and >= 0")
	}
	if candidate.EndSeconds <= candidate.StartSeconds || !finite(candidate.EndSeconds) {
		return ValidationError("segment end must be finite and after start")
	}
	if !slices.Contains(segmentFlavors, candidate.Flavor) {
		return ValidationError("unknown segment flavor " + string(candidate.Flavor))
	}
	if err := validateDetectedFlavor(candidate.SegmentKind, candidate.Flavor); err != nil {
		return err
	}
	if !unitScore(candidate.ClassConfidence) {
		return ValidationError("class confidence must be finite and in [0, 1]")
	}
	if !unitScore(candidate.BoundaryScore) {
		return ValidationError("boundary score must be finite and in [0, 1]")
	}
	if candidate.Flavor == FlavorHostRead && candidate.BoundaryEligible {
		return ValidationError("host-read candidates cannot be boundary-eligible in v1")
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func unitScore(value float64) bool {
	return finite(value) && value >= 0 && value <= 1
}
