package integrationhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/plugin"
)

const (
	PodcastAdModelTaskClass   = "podcast-ad-audio"
	PodcastAdModelHardwareCPU = "cpu"
	PodcastAdWeightsFormat    = "beamfall-podcast-ad-weights-v1"

	modelManifestSchemaVersion = 1
	modelWeightCount           = 15
	modelWeightHeadSize        = 5
	modelWeightsMagic          = "BFPAW1\x00\x00"
	modelWeightsEncodedSize    = len(modelWeightsMagic) + modelWeightCount*8
	maxModelManifestBytes      = 64 << 10
	maxModelTrustBytes         = 16 << 10
)

var (
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
)

var (
	ErrModelArtifactInvalid      = errors.New("integrationhost: invalid model artifact")
	ErrModelArtifactUnsafe       = errors.New("integrationhost: unsafe model artifact")
	ErrModelArtifactIncompatible = errors.New("integrationhost: incompatible model artifact")
)

type ModelRuntimeCompatibility struct {
	MinimumVersion string `json:"minimum_version"`
	MaximumVersion string `json:"maximum_version"`
}

type ModelWeightsVariant struct {
	Hardware string `json:"hardware"`
	Format   string `json:"format"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}

type ModelLicense struct {
	Effective   string `json:"effective"`
	AUP         string `json:"aup"`
	Attribution string `json:"attribution"`
}

type ModelProvenance struct {
	Source               string `json:"source"`
	TrainingCorpusSHA256 string `json:"training_corpus_sha256"`
}

type ModelCalibration struct {
	Flavor                   string  `json:"flavor"`
	HoldoutSHA256            string  `json:"holdout_sha256"`
	HoldoutBoundaryPrecision float64 `json:"holdout_boundary_precision"`
	BoundaryPrecisionFloor   float64 `json:"boundary_precision_floor"`
	HoldoutClassF1           float64 `json:"holdout_class_f1"`
}

type ModelArtifactManifest struct {
	SchemaVersion     int                       `json:"schema_version"`
	ArtifactID        string                    `json:"artifact_id"`
	ModelID           string                    `json:"model_id"`
	Version           string                    `json:"version"`
	TaskClass         string                    `json:"task_class"`
	AuthorKeyID       string                    `json:"author_key_id,omitempty"`
	Runtime           ModelRuntimeCompatibility `json:"runtime"`
	FeatureSetVersion string                    `json:"feature_set_version"`
	Weights           ModelWeightsVariant       `json:"weights"`
	License           ModelLicense              `json:"license"`
	Provenance        ModelProvenance           `json:"provenance"`
	Calibration       ModelCalibration          `json:"calibration"`
}

// PodcastAdModelWeights is the server-owned classifier layout: class,
// start-boundary, and end-boundary heads over
// [bias, RMS, centroid, flatness, rolloff].
type PodcastAdModelWeights struct {
	Class         [modelWeightHeadSize]float64
	StartBoundary [modelWeightHeadSize]float64
	EndBoundary   [modelWeightHeadSize]float64
}

type ModelRuntime struct {
	Version           string
	FeatureSetVersion string
}

type modelTrustSidecar struct {
	AuthorSignature          string `json:"author_signature"`
	ChecksVersion            string `json:"checks_version"`
	RegistryKeyID            string `json:"registry_key_id"`
	RegistryCountersignature string `json:"registry_countersignature"`
}

func decodeModelManifest(raw []byte) (ModelArtifactManifest, []byte, error) {
	if len(raw) == 0 || len(raw) > maxModelManifestBytes {
		return ModelArtifactManifest{}, nil, fmt.Errorf("%w: manifest size", ErrModelArtifactInvalid)
	}
	if err := plugin.RejectDuplicateJSONKeys(raw); err != nil {
		return ModelArtifactManifest{}, nil, fmt.Errorf("%w: manifest JSON: %v", ErrModelArtifactInvalid, err)
	}
	var manifest ModelArtifactManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return ModelArtifactManifest{}, nil, fmt.Errorf("%w: manifest JSON: %v", ErrModelArtifactInvalid, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ModelArtifactManifest{}, nil, fmt.Errorf("%w: manifest trailing JSON", ErrModelArtifactInvalid)
	}
	if err := manifest.validate(); err != nil {
		return ModelArtifactManifest{}, nil, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return ModelArtifactManifest{}, nil, fmt.Errorf("%w: canonical manifest: %v", ErrModelArtifactInvalid, err)
	}
	return manifest, canonical, nil
}

func (m ModelArtifactManifest) validate() error {
	if m.SchemaVersion != modelManifestSchemaVersion {
		return fmt.Errorf("%w: schema version %d", ErrModelArtifactInvalid, m.SchemaVersion)
	}
	for name, value := range map[string]string{
		"artifact id":         m.ArtifactID,
		"model id":            m.ModelID,
		"task class":          m.TaskClass,
		"feature-set version": m.FeatureSetVersion,
		"effective license":   m.License.Effective,
		"AUP":                 m.License.AUP,
		"attribution":         m.License.Attribution,
		"provenance source":   m.Provenance.Source,
		"calibration flavor":  m.Calibration.Flavor,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: %s required", ErrModelArtifactInvalid, name)
		}
	}
	if _, err := parseSemver(m.Version); err != nil {
		return fmt.Errorf("%w: model version: %v", ErrModelArtifactInvalid, err)
	}
	minimum, err := parseSemver(m.Runtime.MinimumVersion)
	if err != nil {
		return fmt.Errorf("%w: minimum runtime version: %v", ErrModelArtifactInvalid, err)
	}
	maximum, err := parseSemver(m.Runtime.MaximumVersion)
	if err != nil {
		return fmt.Errorf("%w: maximum runtime version: %v", ErrModelArtifactInvalid, err)
	}
	if compareSemver(minimum, maximum) > 0 {
		return fmt.Errorf("%w: runtime range is reversed", ErrModelArtifactInvalid)
	}
	if m.TaskClass != PodcastAdModelTaskClass {
		return fmt.Errorf("%w: task class %q", ErrModelArtifactIncompatible, m.TaskClass)
	}
	if m.Weights.Hardware != PodcastAdModelHardwareCPU {
		return fmt.Errorf("%w: hardware variant %q", ErrModelArtifactIncompatible, m.Weights.Hardware)
	}
	if m.Weights.Format != PodcastAdWeightsFormat {
		return fmt.Errorf("%w: weights format %q", ErrModelArtifactUnsafe, m.Weights.Format)
	}
	if m.Weights.Size != int64(modelWeightsEncodedSize) {
		return fmt.Errorf("%w: weights size %d", ErrModelArtifactUnsafe, m.Weights.Size)
	}
	if !validSHA256(m.Weights.SHA256) {
		return fmt.Errorf("%w: weights SHA-256", ErrModelArtifactInvalid)
	}
	if !validSHA256(m.Provenance.TrainingCorpusSHA256) {
		return fmt.Errorf("%w: training corpus SHA-256", ErrModelArtifactInvalid)
	}
	if !validSHA256(m.Calibration.HoldoutSHA256) {
		return fmt.Errorf("%w: holdout SHA-256", ErrModelArtifactInvalid)
	}
	if m.Calibration.Flavor != "inserted" && m.Calibration.Flavor != "host-read" {
		return fmt.Errorf("%w: calibration flavor %q", ErrModelArtifactInvalid, m.Calibration.Flavor)
	}
	for name, value := range map[string]float64{
		"holdout boundary precision": m.Calibration.HoldoutBoundaryPrecision,
		"boundary precision floor":   m.Calibration.BoundaryPrecisionFloor,
		"holdout class F1":           m.Calibration.HoldoutClassF1,
	} {
		if !finiteProbability(value) {
			return fmt.Errorf("%w: calibration %s", ErrModelArtifactInvalid, name)
		}
	}
	return nil
}

func validateModelCompatibility(manifest ModelArtifactManifest, runtime ModelRuntime) error {
	running, err := parseSemver(runtime.Version)
	if err != nil {
		return fmt.Errorf("%w: running runtime version: %v", ErrModelArtifactIncompatible, err)
	}
	minimum, _ := parseSemver(manifest.Runtime.MinimumVersion)
	maximum, _ := parseSemver(manifest.Runtime.MaximumVersion)
	if compareSemver(running, minimum) < 0 || compareSemver(running, maximum) > 0 {
		return fmt.Errorf("%w: runtime %s outside [%s,%s]", ErrModelArtifactIncompatible, runtime.Version, manifest.Runtime.MinimumVersion, manifest.Runtime.MaximumVersion)
	}
	if runtime.FeatureSetVersion == "" || manifest.FeatureSetVersion != runtime.FeatureSetVersion {
		return fmt.Errorf("%w: feature set %q does not match %q", ErrModelArtifactIncompatible, manifest.FeatureSetVersion, runtime.FeatureSetVersion)
	}
	return nil
}

func decodePodcastAdWeights(raw []byte, manifest ModelArtifactManifest) (PodcastAdModelWeights, error) {
	if int64(len(raw)) != manifest.Weights.Size || len(raw) != modelWeightsEncodedSize {
		return PodcastAdModelWeights{}, fmt.Errorf("%w: weights size mismatch", ErrModelArtifactUnsafe)
	}
	if !bytes.Equal(raw[:len(modelWeightsMagic)], []byte(modelWeightsMagic)) {
		return PodcastAdModelWeights{}, fmt.Errorf("%w: weights magic", ErrModelArtifactUnsafe)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != manifest.Weights.SHA256 {
		return PodcastAdModelWeights{}, fmt.Errorf("%w: weights SHA-256 mismatch", ErrModelArtifactInvalid)
	}
	var values [modelWeightCount]float64
	for i := range values {
		offset := len(modelWeightsMagic) + i*8
		values[i] = math.Float64frombits(binary.LittleEndian.Uint64(raw[offset : offset+8]))
		if math.IsNaN(values[i]) || math.IsInf(values[i], 0) {
			return PodcastAdModelWeights{}, fmt.Errorf("%w: non-finite weight", ErrModelArtifactUnsafe)
		}
	}
	var weights PodcastAdModelWeights
	copy(weights.Class[:], values[:modelWeightHeadSize])
	copy(weights.StartBoundary[:], values[modelWeightHeadSize:modelWeightHeadSize*2])
	copy(weights.EndBoundary[:], values[modelWeightHeadSize*2:])
	return weights, nil
}

func decodeModelTrustSidecar(raw []byte) (modelTrustSidecar, error) {
	if len(raw) == 0 || len(raw) > maxModelTrustBytes {
		return modelTrustSidecar{}, errors.New("model trust sidecar size")
	}
	if err := plugin.RejectDuplicateJSONKeys(raw); err != nil {
		return modelTrustSidecar{}, err
	}
	var sidecar modelTrustSidecar
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sidecar); err != nil {
		return modelTrustSidecar{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return modelTrustSidecar{}, errors.New("model trust sidecar trailing JSON")
	}
	return sidecar, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func finiteProbability(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

type semver struct {
	major, minor, patch uint64
	prerelease          []string
}

func parseSemver(value string) (semver, error) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return semver{}, fmt.Errorf("%q is not SemVer", value)
	}
	parts := [3]uint64{}
	for i := range parts {
		parsed, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return semver{}, fmt.Errorf("%q is not SemVer", value)
		}
		parts[i] = parsed
	}
	var prerelease []string
	if match[4] != "" {
		prerelease = strings.Split(match[4], ".")
	}
	return semver{major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease}, nil
}

func compareSemver(left, right semver) int {
	for _, pair := range [][2]uint64{
		{left.major, right.major},
		{left.minor, right.minor},
		{left.patch, right.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(left.prerelease) && i < len(right.prerelease); i++ {
		if compared := compareSemverIdentifier(left.prerelease[i], right.prerelease[i]); compared != 0 {
			return compared
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func compareSemverIdentifier(left, right string) int {
	leftNumeric := decimalIdentifier(left)
	rightNumeric := decimalIdentifier(right)
	if leftNumeric && rightNumeric {
		if len(left) < len(right) {
			return -1
		}
		if len(left) > len(right) {
			return 1
		}
		return strings.Compare(left, right)
	}
	if leftNumeric {
		return -1
	}
	if rightNumeric {
		return 1
	}
	return strings.Compare(left, right)
}

func decimalIdentifier(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}
