package integrationhost

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestModelArtifactDecodesFixedNumericWeights(t *testing.T) {
	raw := testPodcastAdWeights()
	manifest := testModelManifest(raw)
	manifestRaw := mustJSON(t, manifest)

	decoded, canonical, err := decodeModelManifest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	weights, err := decodePodcastAdWeights(raw, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if weights.Class != [5]float64{-5, 0, 0, 10, 0} {
		t.Fatalf("class weights = %v", weights.Class)
	}
	if weights.StartBoundary != [5]float64{0, 0, 0.01, 0, 0} {
		t.Fatalf("start weights = %v", weights.StartBoundary)
	}
	if weights.EndBoundary != [5]float64{0, 0, 0, 0, 0.01} {
		t.Fatalf("end weights = %v", weights.EndBoundary)
	}
	second, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(canonical) != string(second) {
		t.Fatalf("canonical manifest = %s, want %s", canonical, second)
	}
}

func TestModelManifestValidationFailsClosed(t *testing.T) {
	weights := testPodcastAdWeights()
	tests := map[string]func(*ModelArtifactManifest){
		"schema":             func(m *ModelArtifactManifest) { m.SchemaVersion++ },
		"artifact id":        func(m *ModelArtifactManifest) { m.ArtifactID = "" },
		"model id":           func(m *ModelArtifactManifest) { m.ModelID = "" },
		"model semver":       func(m *ModelArtifactManifest) { m.Version = "v1" },
		"task":               func(m *ModelArtifactManifest) { m.TaskClass = "embedding" },
		"runtime minimum":    func(m *ModelArtifactManifest) { m.Runtime.MinimumVersion = "1" },
		"runtime maximum":    func(m *ModelArtifactManifest) { m.Runtime.MaximumVersion = "1.0" },
		"runtime range":      func(m *ModelArtifactManifest) { m.Runtime.MinimumVersion = "2.0.0" },
		"feature set":        func(m *ModelArtifactManifest) { m.FeatureSetVersion = "" },
		"hardware":           func(m *ModelArtifactManifest) { m.Weights.Hardware = "cuda" },
		"format":             func(m *ModelArtifactManifest) { m.Weights.Format = "pickle" },
		"size":               func(m *ModelArtifactManifest) { m.Weights.Size++ },
		"hash":               func(m *ModelArtifactManifest) { m.Weights.SHA256 = strings.Repeat("A", 64) },
		"license":            func(m *ModelArtifactManifest) { m.License.Effective = "" },
		"AUP":                func(m *ModelArtifactManifest) { m.License.AUP = "" },
		"attribution":        func(m *ModelArtifactManifest) { m.License.Attribution = "" },
		"provenance":         func(m *ModelArtifactManifest) { m.Provenance.Source = "" },
		"corpus hash":        func(m *ModelArtifactManifest) { m.Provenance.TrainingCorpusSHA256 = "bad" },
		"flavor":             func(m *ModelArtifactManifest) { m.Calibration.Flavor = "unknown" },
		"holdout hash":       func(m *ModelArtifactManifest) { m.Calibration.HoldoutSHA256 = "bad" },
		"boundary precision": func(m *ModelArtifactManifest) { m.Calibration.HoldoutBoundaryPrecision = 2 },
		"boundary floor":     func(m *ModelArtifactManifest) { m.Calibration.BoundaryPrecisionFloor = 2 },
		"class F1":           func(m *ModelArtifactManifest) { m.Calibration.HoldoutClassF1 = -1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := testModelManifest(weights)
			mutate(&manifest)
			if _, _, err := decodeModelManifest(mustJSON(t, manifest)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestModelManifestRejectsAmbiguousJSON(t *testing.T) {
	raw := mustJSON(t, testModelManifest(testPodcastAdWeights()))
	duplicate := []byte(strings.Replace(string(raw), `"artifact_id":"podcast-ad-audio-cpu"`, `"artifact_id":"podcast-ad-audio-cpu","artifact_id":"replacement"`, 1))
	unknown := []byte(strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"code":"run-me"`, 1))
	trailing := append(append([]byte(nil), raw...), []byte("{}")...)
	for name, candidate := range map[string][]byte{
		"duplicate": duplicate,
		"unknown":   unknown,
		"trailing":  trailing,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeModelManifest(candidate); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestModelWeightsRejectCodeBearingAndCorruptBytes(t *testing.T) {
	valid := testPodcastAdWeights()
	tests := map[string][]byte{
		"pickle":      append([]byte{0x80, 0x04}, make([]byte, modelWeightsEncodedSize-2)...),
		"ELF":         append([]byte("\x7fELF"), make([]byte, modelWeightsEncodedSize-4)...),
		"WebAssembly": append([]byte("\x00asm"), make([]byte, modelWeightsEncodedSize-4)...),
		"PE":          append([]byte("MZ"), make([]byte, modelWeightsEncodedSize-2)...),
		"Mach-O":      append([]byte{0xfe, 0xed, 0xfa, 0xcf}, make([]byte, modelWeightsEncodedSize-4)...),
		"script":      append([]byte("#!/bin/sh"), make([]byte, modelWeightsEncodedSize-9)...),
		"truncated":   valid[:len(valid)-1],
		"extra data":  append(append([]byte(nil), valid...), 0),
	}
	nonFinite := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint64(nonFinite[len(modelWeightsMagic):], math.Float64bits(math.Inf(1)))
	tests["non-finite"] = nonFinite

	for name, candidate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := testModelManifest(candidate)
			manifest.Weights.Size = int64(len(candidate))
			if len(candidate) != modelWeightsEncodedSize {
				manifest.Weights.Size = int64(modelWeightsEncodedSize)
			}
			if _, err := decodePodcastAdWeights(candidate, manifest); !errors.Is(err, ErrModelArtifactUnsafe) {
				t.Fatalf("error = %v, want unsafe artifact", err)
			}
		})
	}

	manifest := testModelManifest(valid)
	manifest.Weights.SHA256 = strings.Repeat("0", 64)
	if _, err := decodePodcastAdWeights(valid, manifest); !errors.Is(err, ErrModelArtifactInvalid) {
		t.Fatalf("hash error = %v", err)
	}
}

func TestModelWeightsAcceptExecutableMagicInsideFiniteValues(t *testing.T) {
	raw := testPodcastAdWeights()
	fragments := [][]byte{
		[]byte("MZ"),
		[]byte("\x7fELF"),
		[]byte("\x00asm"),
		[]byte("#!"),
		{0xfe, 0xed, 0xfa, 0xcf},
	}
	for i, fragment := range fragments {
		offset := len(modelWeightsMagic) + i*8
		copy(raw[offset:offset+len(fragment)], fragment)
	}
	manifest := testModelManifest(raw)
	if _, err := decodePodcastAdWeights(raw, manifest); err != nil {
		t.Fatalf("decode finite numeric payload: %v", err)
	}
}

func TestSemverCompatibilityIncludesPrereleaseOrdering(t *testing.T) {
	manifest := testModelManifest(testPodcastAdWeights())
	manifest.Runtime.MinimumVersion = "1.2.0-beta.1"
	manifest.Runtime.MaximumVersion = "1.2.0"
	if err := validateModelCompatibility(manifest, ModelRuntime{
		Version:           "1.2.0-beta.2",
		FeatureSetVersion: manifest.FeatureSetVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateModelCompatibility(manifest, ModelRuntime{
		Version:           "1.2.1",
		FeatureSetVersion: manifest.FeatureSetVersion,
	}); !errors.Is(err, ErrModelArtifactIncompatible) {
		t.Fatalf("error = %v, want incompatibility", err)
	}

	manifest.Runtime.MinimumVersion = "1.2.0-100000000000000000000"
	manifest.Runtime.MaximumVersion = "1.2.0-99999999999999999999"
	if err := manifest.validate(); !errors.Is(err, ErrModelArtifactInvalid) {
		t.Fatalf("oversized reversed numeric range error = %v", err)
	}
	left, err := parseSemver("1.2.0-99999999999999999999")
	if err != nil {
		t.Fatal(err)
	}
	right, err := parseSemver("1.2.0-100000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if compareSemver(left, right) >= 0 {
		t.Fatalf("oversized numeric prerelease ordering is not numeric")
	}
}

func testPodcastAdWeights() []byte {
	values := [modelWeightCount]float64{
		-5, 0, 0, 10, 0,
		0, 0, 0.01, 0, 0,
		0, 0, 0, 0, 0.01,
	}
	raw := make([]byte, modelWeightsEncodedSize)
	copy(raw, modelWeightsMagic)
	for i, value := range values {
		offset := len(modelWeightsMagic) + i*8
		binary.LittleEndian.PutUint64(raw[offset:offset+8], math.Float64bits(value))
	}
	return raw
}

func testModelManifest(weights []byte) ModelArtifactManifest {
	digest := sha256.Sum256(weights)
	return ModelArtifactManifest{
		SchemaVersion:     modelManifestSchemaVersion,
		ArtifactID:        "podcast-ad-audio-cpu",
		ModelID:           "podcast-ad-audio",
		Version:           "1.0.0",
		TaskClass:         PodcastAdModelTaskClass,
		AuthorKeyID:       "author-model-1",
		Runtime:           ModelRuntimeCompatibility{MinimumVersion: "1.0.0", MaximumVersion: "1.9.9"},
		FeatureSetVersion: "audio-ad-audio-features-v1",
		Weights: ModelWeightsVariant{
			Hardware: PodcastAdModelHardwareCPU,
			Format:   PodcastAdWeightsFormat,
			Size:     int64(len(weights)),
			SHA256:   hex.EncodeToString(digest[:]),
		},
		License: ModelLicense{
			Effective:   "Beamfall Model Weights License",
			AUP:         "No prohibited use",
			Attribution: "Beamfall podcast-ad-audio",
		},
		Provenance: ModelProvenance{
			Source:               "published training corpus",
			TrainingCorpusSHA256: strings.Repeat("a", 64),
		},
		Calibration: ModelCalibration{
			Flavor:                   "inserted",
			HoldoutSHA256:            strings.Repeat("b", 64),
			HoldoutBoundaryPrecision: 0.99,
			BoundaryPrecisionFloor:   0.95,
			HoldoutClassF1:           0.8,
		},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
