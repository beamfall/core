package integrationhost

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/beamfall/core/internal/plugin"
)

const (
	ModelManifestFilename = "manifest.json"
	ModelWeightsFilename  = "weights.bfpaw"
	ModelTrustFilename    = "trust.json"
)

type ModelTrustLabel string

const (
	ModelTrustTrusted      ModelTrustLabel = "trusted"
	ModelTrustTampered     ModelTrustLabel = "tampered"
	ModelTrustRevoked      ModelTrustLabel = "revoked"
	ModelTrustUnrecognized ModelTrustLabel = "unrecognized"
	ModelTrustUnsupported  ModelTrustLabel = "unsupported"
)

// ModelProjection is pure manifest and numeric classifier data. A composition
// root may adapt it to a task implementation without the loader importing that
// implementation.
type ModelProjection struct {
	Manifest          ModelArtifactManifest
	Weights           PodcastAdModelWeights
	Trust             ModelTrustLabel
	CanonicalManifest []byte
}

type ModelLoader struct {
	runtime ModelRuntime
	roots   plugin.PluginTrustRoots
	revoked []string

	mu     sync.RWMutex
	active *ModelProjection
}

func NewModelLoader(runtime ModelRuntime, roots plugin.PluginTrustRoots, revokedAuthorKeyIDs []string) (*ModelLoader, error) {
	if _, err := parseSemver(runtime.Version); err != nil {
		return nil, fmt.Errorf("%w: running runtime version: %v", ErrModelArtifactIncompatible, err)
	}
	if runtime.FeatureSetVersion == "" {
		return nil, fmt.Errorf("%w: running feature-set version required", ErrModelArtifactIncompatible)
	}
	return &ModelLoader{
		runtime: runtime,
		roots:   roots,
		revoked: append([]string(nil), revokedAuthorKeyIDs...),
	}, nil
}

// ActivateCached validates and activates only local cache bytes. It performs no
// registry lookup and preserves the active last-good projection on failure.
func (l *ModelLoader) ActivateCached(root string) (ModelProjection, error) {
	return l.activate(root)
}

// ActivateSideload applies the same safety and compatibility checks as cached
// activation. An absent trust sidecar is accepted and labeled unsupported.
func (l *ModelLoader) ActivateSideload(root string) (ModelProjection, error) {
	return l.activate(root)
}

func (l *ModelLoader) Active() (ModelProjection, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.active == nil {
		return ModelProjection{}, false
	}
	return cloneModelProjection(*l.active), true
}

func (l *ModelLoader) activate(root string) (ModelProjection, error) {
	candidate, err := l.load(root)
	if err != nil {
		return ModelProjection{}, err
	}
	l.mu.Lock()
	l.active = &candidate
	l.mu.Unlock()
	return cloneModelProjection(candidate), nil
}

func (l *ModelLoader) load(root string) (ModelProjection, error) {
	if err := CheckLocalInferenceModelRoot(root); err != nil {
		return ModelProjection{}, fmt.Errorf("%w: model root: %v", ErrModelArtifactInvalid, err)
	}
	manifestRaw, err := readBoundedRegularFile(filepath.Join(root, ModelManifestFilename), maxModelManifestBytes)
	if err != nil {
		return ModelProjection{}, fmt.Errorf("%w: read manifest: %v", ErrModelArtifactInvalid, err)
	}
	manifest, canonicalManifest, err := decodeModelManifest(manifestRaw)
	if err != nil {
		return ModelProjection{}, err
	}
	if err := validateModelCompatibility(manifest, l.runtime); err != nil {
		return ModelProjection{}, err
	}
	weightsRaw, err := readBoundedRegularFile(filepath.Join(root, ModelWeightsFilename), modelWeightsEncodedSize)
	if err != nil {
		return ModelProjection{}, fmt.Errorf("%w: read weights: %v", ErrModelArtifactInvalid, err)
	}
	weights, err := decodePodcastAdWeights(weightsRaw, manifest)
	if err != nil {
		return ModelProjection{}, err
	}
	trust := l.assessTrust(root, weightsRaw, canonicalManifest, manifest)
	return ModelProjection{
		Manifest:          manifest,
		Weights:           weights,
		Trust:             trust,
		CanonicalManifest: canonicalManifest,
	}, nil
}

func (l *ModelLoader) assessTrust(
	root string,
	weights, canonicalManifest []byte,
	manifest ModelArtifactManifest,
) ModelTrustLabel {
	raw, present, err := readOptionalBoundedRegularFile(filepath.Join(root, ModelTrustFilename), maxModelTrustBytes)
	if err != nil {
		return ModelTrustTampered
	}
	if !present {
		assessment := plugin.AssessArtifactTrust(weights, canonicalManifest, nil, l.roots, l.revoked)
		return modelTrustLabel(assessment.State)
	}
	sidecar, err := decodeModelTrustSidecar(raw)
	if err != nil {
		return ModelTrustTampered
	}
	assessment := plugin.AssessArtifactTrust(weights, canonicalManifest, &plugin.ArtifactTrustEnvelope{
		ArtifactSHA256:           manifest.Weights.SHA256,
		AuthorKeyID:              manifest.AuthorKeyID,
		AuthorSignature:          sidecar.AuthorSignature,
		ChecksVersion:            sidecar.ChecksVersion,
		RegistryKeyID:            sidecar.RegistryKeyID,
		RegistryCountersignature: sidecar.RegistryCountersignature,
	}, l.roots, l.revoked)
	return modelTrustLabel(assessment.State)
}

func modelTrustLabel(state plugin.PluginTrustState) ModelTrustLabel {
	switch state {
	case plugin.PluginTrusted:
		return ModelTrustTrusted
	case plugin.PluginTampered:
		return ModelTrustTampered
	case plugin.PluginRevoked:
		return ModelTrustRevoked
	case plugin.PluginUnrecognized:
		return ModelTrustUnrecognized
	default:
		return ModelTrustUnsupported
	}
}

func cloneModelProjection(projection ModelProjection) ModelProjection {
	projection.CanonicalManifest = append([]byte(nil), projection.CanonicalManifest...)
	return projection
}

func readOptionalBoundedRegularFile(path string, maximum int) ([]byte, bool, error) {
	raw, err := readBoundedRegularFile(path, maximum)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	return raw, true, err
}

func readBoundedRegularFile(path string, maximum int) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("artifact path is not a regular file")
	}
	if before.Size() <= 0 || before.Size() > int64(maximum) {
		return nil, fmt.Errorf("artifact size %d exceeds bound %d", before.Size(), maximum)
	}
	file, err := os.Open(path) // #nosec G304 -- path is a fixed filename under a validated private root.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, errors.New("artifact changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > maximum {
		return nil, fmt.Errorf("artifact size %d exceeds bound %d", len(raw), maximum)
	}
	finished, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !sameFileSnapshot(after, finished) || int64(len(raw)) != finished.Size() {
		return nil, errors.New("artifact changed while reading")
	}
	return raw, nil
}

func sameFileSnapshot(before, after os.FileInfo) bool {
	return os.SameFile(before, after) &&
		before.Size() == after.Size() &&
		before.ModTime().Equal(after.ModTime())
}
