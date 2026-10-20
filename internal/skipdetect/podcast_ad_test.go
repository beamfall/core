package skipdetect

import (
	"math"
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
)

func audioAdTestModel(flavor store.SegmentFlavor) AudioAdModel {
	return AudioAdModel{
		ModelID:              "audio-ad-audio",
		ModelVersion:         "1.0.0",
		FeatureSetVersion:    ffmpeg.AudioAdFeatureSetVersion,
		Flavor:               flavor,
		ClassWeights:         AudioAdWeights{-5, 0, 0, 10, 0},
		StartBoundaryWeights: AudioAdWeights{0, 0, 0.01, 0, 0},
		EndBoundaryWeights:   AudioAdWeights{0, 0, 0, 0, 0.01},
	}
}

func audioAdTestFeatures() ffmpeg.AudioAdFeatureSet {
	return ffmpeg.AudioAdFeatureSet{
		Version:       ffmpeg.AudioAdFeatureSetVersion,
		WindowSeconds: ffmpeg.AudioAdFeatureWindowSeconds,
		Frames: []ffmpeg.AudioAdFeatureFrame{
			{StartSeconds: 0, RMSDBFS: -20, SpectralCentroidHz: 100, SpectralFlatness: 0.8, SpectralRolloffHz: 100},
			{StartSeconds: 1, RMSDBFS: -18, SpectralCentroidHz: 300, SpectralFlatness: 0.8, SpectralRolloffHz: 200},
			{StartSeconds: 2, RMSDBFS: -19, SpectralCentroidHz: 200, SpectralFlatness: 0.8, SpectralRolloffHz: 400},
			{StartSeconds: 3, RMSDBFS: -22, SpectralCentroidHz: 50, SpectralFlatness: 0.1, SpectralRolloffHz: 50},
		},
	}
}

func TestAudioAdModelInferenceIsDeterministicAndBoundaryRefined(t *testing.T) {
	model := audioAdTestModel(store.FlavorInserted)
	features := audioAdTestFeatures()
	first, err := model.Infer(features)
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.Infer(features)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("inference changed: %+v vs %+v", first, second)
	}
	if len(first) != 1 || first[0].StartSeconds != 1 || first[0].EndSeconds != 3 {
		t.Fatalf("candidates = %+v", first)
	}
	if first[0].ClassConfidence <= 0.5 || first[0].BoundaryScore <= 0.5 {
		t.Fatalf("scores = %+v", first[0])
	}
	model.StartBoundaryWeights = AudioAdWeights{}
	model.EndBoundaryWeights = AudioAdWeights{}
	coarse, err := model.Infer(features)
	if err != nil {
		t.Fatal(err)
	}
	if coarse[0].StartSeconds != 0 || coarse[0].EndSeconds != 1 {
		t.Fatalf("boundary-only endpoint change = %+v", coarse)
	}
}

func TestAudioAdModelValidationFailsClosed(t *testing.T) {
	tests := map[string]func(*AudioAdModel){
		"missing id":       func(model *AudioAdModel) { model.ModelID = "" },
		"missing version":  func(model *AudioAdModel) { model.ModelVersion = "" },
		"feature mismatch": func(model *AudioAdModel) { model.FeatureSetVersion = "v2" },
		"unknown flavor":   func(model *AudioAdModel) { model.Flavor = "unknown" },
		"non-finite weight": func(model *AudioAdModel) {
			model.ClassWeights[0] = math.NaN()
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			model := audioAdTestModel(store.FlavorInserted)
			mutate(&model)
			if err := model.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestAudioAdInferenceRejectsInvalidFramesAndScores(t *testing.T) {
	model := audioAdTestModel(store.FlavorInserted)
	tests := map[string]func(*ffmpeg.AudioAdFeatureSet){
		"feature mismatch": func(features *ffmpeg.AudioAdFeatureSet) { features.Version = "v2" },
		"non-finite frame": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].RMSDBFS = math.Inf(1)
		},
		"gapped frame": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[1].StartSeconds = 1.5
		},
		"invalid flatness": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].SpectralFlatness = 2
		},
		"empty feature set": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames = nil
		},
		"positive RMS": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].RMSDBFS = 5
		},
		"RMS below silence floor": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].RMSDBFS = -200
		},
		"centroid above Nyquist ceiling": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].SpectralCentroidHz = audioAdSpectralNyquistHz + 1
		},
		"rolloff above Nyquist ceiling": func(features *ffmpeg.AudioAdFeatureSet) {
			features.Frames[0].SpectralRolloffHz = audioAdSpectralNyquistHz + 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			features := audioAdTestFeatures()
			mutate(&features)
			if _, err := model.Infer(features); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	model.ClassWeights[2] = math.MaxFloat64
	if _, err := model.Infer(audioAdTestFeatures()); err == nil {
		t.Fatal("non-finite score was accepted")
	}
}

func TestAudioAdInferenceAcceptsSpectralValuesAtNyquistCeiling(t *testing.T) {
	model := audioAdTestModel(store.FlavorInserted)
	features := audioAdTestFeatures()
	features.Frames[0].SpectralCentroidHz = audioAdSpectralNyquistHz
	features.Frames[0].SpectralRolloffHz = audioAdSpectralNyquistHz
	if _, err := model.Infer(features); err != nil {
		t.Fatalf("spectral value exactly at the Nyquist ceiling was rejected: %v", err)
	}
}

func TestAudioAdAdmissionUsesBoundaryCalibrationNotClassF1(t *testing.T) {
	candidate := AudioAdCandidate{
		ModelID:           "audio-ad-audio",
		ModelVersion:      "1.0.0",
		FeatureSetVersion: ffmpeg.AudioAdFeatureSetVersion,
		Flavor:            store.FlavorInserted,
		StartSeconds:      10,
		EndSeconds:        20,
		ClassConfidence:   0.01,
		BoundaryScore:     0.01,
	}
	calibration := AudioAdCalibration{
		ModelID:                  candidate.ModelID,
		ModelVersion:             candidate.ModelVersion,
		FeatureSetVersion:        candidate.FeatureSetVersion,
		Flavor:                   candidate.Flavor,
		HoldoutBoundaryPrecision: 0.99,
		BoundaryPrecisionFloor:   0.95,
		HoldoutClassF1:           0.01,
	}
	admit, err := calibration.admits(candidate)
	if err != nil || !admit {
		t.Fatalf("high boundary/low F1 = %v, %v", admit, err)
	}
	calibration.HoldoutBoundaryPrecision = 0.90
	calibration.HoldoutClassF1 = 1
	candidate.ClassConfidence = 1
	candidate.BoundaryScore = 1
	admit, err = calibration.admits(candidate)
	if err != nil || admit {
		t.Fatalf("low boundary/high F1 = %v, %v", admit, err)
	}
}

func TestAudioAdHostReadNeverAdmitted(t *testing.T) {
	candidate := AudioAdCandidate{
		ModelID:           "audio-ad-audio",
		ModelVersion:      "1.0.0",
		FeatureSetVersion: ffmpeg.AudioAdFeatureSetVersion,
		Flavor:            store.FlavorHostRead,
		StartSeconds:      10,
		EndSeconds:        20,
		ClassConfidence:   1,
		BoundaryScore:     1,
	}
	calibration := AudioAdCalibration{
		ModelID:                  candidate.ModelID,
		ModelVersion:             candidate.ModelVersion,
		FeatureSetVersion:        candidate.FeatureSetVersion,
		Flavor:                   candidate.Flavor,
		HoldoutBoundaryPrecision: 1,
		BoundaryPrecisionFloor:   0,
		HoldoutClassF1:           1,
	}
	admit, err := calibration.admits(candidate)
	if err != nil || admit {
		t.Fatalf("host-read admitted = %v, %v", admit, err)
	}
}

func TestAudioAdCalibrationAndCandidateValidationFailClosed(t *testing.T) {
	candidate := AudioAdCandidate{
		ModelID:           "audio-ad-audio",
		ModelVersion:      "1.0.0",
		FeatureSetVersion: ffmpeg.AudioAdFeatureSetVersion,
		Flavor:            store.FlavorInserted,
		StartSeconds:      20,
		EndSeconds:        10,
		ClassConfidence:   0.9,
		BoundaryScore:     0.9,
	}
	calibration := AudioAdCalibration{
		ModelID:                  candidate.ModelID,
		ModelVersion:             candidate.ModelVersion,
		FeatureSetVersion:        candidate.FeatureSetVersion,
		Flavor:                   candidate.Flavor,
		HoldoutBoundaryPrecision: 0.99,
		BoundaryPrecisionFloor:   0.95,
		HoldoutClassF1:           0.9,
	}
	if _, err := calibration.admits(candidate); err == nil {
		t.Fatal("invalid range was accepted")
	}
	candidate.StartSeconds, candidate.EndSeconds = 10, 20
	candidate.ClassConfidence = math.NaN()
	if _, err := calibration.admits(candidate); err == nil {
		t.Fatal("invalid score was accepted")
	}
	candidate.ClassConfidence = 0.9
	calibration.HoldoutBoundaryPrecision = math.Inf(1)
	if _, err := calibration.admits(candidate); err == nil {
		t.Fatal("invalid calibration was accepted")
	}
	calibration.HoldoutBoundaryPrecision = 0.99
	calibration.Flavor = "unknown"
	if _, err := calibration.admits(candidate); err == nil {
		t.Fatal("unknown flavor was accepted")
	}
	calibration.Flavor = store.FlavorInserted
	calibration.ModelID = ""
	if _, err := calibration.admits(candidate); err == nil {
		t.Fatal("missing calibration identity was accepted")
	}
}
