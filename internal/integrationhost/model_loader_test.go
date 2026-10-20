package integrationhost

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/beamfall/core/internal/plugin"
)

func TestModelLoaderActivatesSignedCachedArtifactOffline(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	roots, trust := signedModelTrust(t, manifest, weights)
	root := writeModelArtifact(t, manifest, weights, trust, false)
	loader := newTestModelLoader(t, roots)

	projection, err := loader.ActivateCached(root)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Trust != ModelTrustTrusted {
		t.Fatalf("trust = %q", projection.Trust)
	}
	if projection.Manifest.ArtifactID != manifest.ArtifactID || projection.Manifest.ModelID != manifest.ModelID {
		t.Fatalf("projection manifest = %+v", projection.Manifest)
	}
	if projection.Weights.Class != [5]float64{-5, 0, 0, 10, 0} {
		t.Fatalf("projection weights = %+v", projection.Weights)
	}
	active, ok := loader.Active()
	if !ok || !reflect.DeepEqual(active, projection) {
		t.Fatalf("active = %+v, %v", active, ok)
	}
}

func TestModelLoaderUnsignedSideloadIsUnsupported(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	manifest.AuthorKeyID = ""
	root := writeModelArtifact(t, manifest, weights, nil, false)
	loader := newTestModelLoader(t, plugin.PluginTrustRoots{})

	projection, err := loader.ActivateSideload(root)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Trust != ModelTrustUnsupported {
		t.Fatalf("trust = %q, want unsupported", projection.Trust)
	}
}

func TestModelLoaderTamperedTrustIsAdvisory(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	roots, trust := signedModelTrust(t, manifest, weights)
	var sidecar map[string]any
	if err := json.Unmarshal(trust, &sidecar); err != nil {
		t.Fatal(err)
	}
	sidecar["author_signature"] = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, ed25519.SignatureSize))
	root := writeModelArtifact(t, manifest, weights, mustJSON(t, sidecar), false)
	loader := newTestModelLoader(t, roots)

	projection, err := loader.ActivateSideload(root)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Trust != ModelTrustTampered {
		t.Fatalf("trust = %q, want tampered", projection.Trust)
	}
}

func TestModelLoaderPreservesLastGoodOnRejectedCandidate(t *testing.T) {
	weights := testPodcastAdWeights()
	goodManifest := testModelManifest(weights)
	goodRoot := writeModelArtifact(t, goodManifest, weights, nil, false)
	loader := newTestModelLoader(t, plugin.PluginTrustRoots{})
	good, err := loader.ActivateCached(goodRoot)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]func(*ModelArtifactManifest, []byte) []byte{
		"runtime mismatch": func(manifest *ModelArtifactManifest, weights []byte) []byte {
			manifest.ArtifactID = "runtime-mismatch"
			manifest.Runtime.MinimumVersion = "2.0.0"
			manifest.Runtime.MaximumVersion = "2.9.9"
			return weights
		},
		"feature mismatch": func(manifest *ModelArtifactManifest, weights []byte) []byte {
			manifest.ArtifactID = "feature-mismatch"
			manifest.FeatureSetVersion = "audio-ad-audio-features-v2"
			return weights
		},
		"corrupted cache": func(manifest *ModelArtifactManifest, weights []byte) []byte {
			manifest.ArtifactID = "corrupt-cache"
			corrupt := append([]byte(nil), weights...)
			corrupt[len(corrupt)-1] ^= 0xff
			return corrupt
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest := testModelManifest(weights)
			candidateWeights := mutate(&manifest, weights)
			root := writeModelArtifact(t, manifest, candidateWeights, nil, false)
			if _, err := loader.ActivateCached(root); err == nil {
				t.Fatal("expected activation error")
			}
			active, ok := loader.Active()
			if !ok || !reflect.DeepEqual(active, good) {
				t.Fatalf("last-good changed: %+v, %v", active, ok)
			}
		})
	}
}

func TestModelLoaderDeterministicManifestProjection(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	compactRoot := writeModelArtifact(t, manifest, weights, nil, false)
	prettyRoot := writeModelArtifact(t, manifest, weights, nil, true)
	firstLoader := newTestModelLoader(t, plugin.PluginTrustRoots{})
	secondLoader := newTestModelLoader(t, plugin.PluginTrustRoots{})

	first, err := firstLoader.ActivateCached(compactRoot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondLoader.ActivateCached(prettyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalManifest, second.CanonicalManifest) {
		t.Fatalf("canonical manifests differ:\n%s\n%s", first.CanonicalManifest, second.CanonicalManifest)
	}
	first.CanonicalManifest[0] = '!'
	active, ok := firstLoader.Active()
	if !ok || active.CanonicalManifest[0] == '!' {
		t.Fatal("caller mutated active projection")
	}
}

func TestModelLoaderMalformedTrustSidecarIsTampered(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	root := writeModelArtifact(t, manifest, weights, []byte(`{"author_signature":"duplicate","author_signature":"duplicate"}`), false)
	loader := newTestModelLoader(t, plugin.PluginTrustRoots{})
	projection, err := loader.ActivateCached(root)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Trust != ModelTrustTampered {
		t.Fatalf("trust = %q", projection.Trust)
	}
}

func TestModelLoaderRejectsNonPrivateRootAndSymlinkedArtifact(t *testing.T) {
	weights := testPodcastAdWeights()
	manifest := testModelManifest(weights)
	root := writeModelArtifact(t, manifest, weights, nil, false)
	loader := newTestModelLoader(t, plugin.PluginTrustRoots{})

	//nolint:gosec // test fixture: non-private directory mode must be rejected.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ActivateCached(root); !errors.Is(err, ErrModelArtifactInvalid) {
		t.Fatalf("root error = %v", err)
	}
	//nolint:gosec // test fixture: private directories require owner execute permission.
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, ModelManifestFilename)
	realManifest := manifestPath + ".real"
	if err := os.Rename(manifestPath, realManifest); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realManifest, manifestPath); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.ActivateCached(root); !errors.Is(err, ErrModelArtifactInvalid) {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestFileSnapshotDetectsSameFileSameLengthRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replaced"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := before.ModTime().Add(time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() {
		t.Fatal("test did not preserve file identity and size")
	}
	if sameFileSnapshot(before, after) {
		t.Fatal("same-length in-place refresh appeared unchanged")
	}
}

func newTestModelLoader(t *testing.T, roots plugin.PluginTrustRoots) *ModelLoader {
	t.Helper()
	loader, err := NewModelLoader(ModelRuntime{
		Version:           "1.2.0",
		FeatureSetVersion: "audio-ad-audio-features-v1",
	}, roots, nil)
	if err != nil {
		t.Fatal(err)
	}
	return loader
}

func writeModelArtifact(
	t *testing.T,
	manifest ModelArtifactManifest,
	weights, trust []byte,
	pretty bool,
) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "model")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	var manifestRaw []byte
	var err error
	if pretty {
		manifestRaw, err = json.MarshalIndent(manifest, "", "  ")
	} else {
		manifestRaw, err = json.Marshal(manifest)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ModelManifestFilename), manifestRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ModelWeightsFilename), weights, 0o600); err != nil {
		t.Fatal(err)
	}
	if trust != nil {
		if err := os.WriteFile(filepath.Join(root, ModelTrustFilename), trust, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func signedModelTrust(
	t *testing.T,
	manifest ModelArtifactManifest,
	weights []byte,
) (plugin.PluginTrustRoots, []byte) {
	t.Helper()
	authorPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	registryPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize))
	canonicalManifest := mustJSON(t, manifest)
	authorSignature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(authorPrivate, canonicalManifest))
	var genericManifest any
	if err := json.Unmarshal(canonicalManifest, &genericManifest); err != nil {
		t.Fatal(err)
	}
	registryPayload := mustJSON(t, map[string]any{
		"author_signature": authorSignature,
		"manifest":         genericManifest,
	})
	trust := mustJSON(t, modelTrustSidecar{
		AuthorSignature:          authorSignature,
		ChecksVersion:            "1",
		RegistryKeyID:            "registry-model-1",
		RegistryCountersignature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(registryPrivate, registryPayload)),
	})
	return plugin.PluginTrustRoots{
		Authors: map[string]ed25519.PublicKey{manifest.AuthorKeyID: authorPrivate.Public().(ed25519.PublicKey)},
		Registries: map[string]plugin.RegistryTrustRoot{"registry-model-1": {
			PublicKey:      registryPrivate.Public().(ed25519.PublicKey),
			ChecksVersions: map[string]struct{}{"1": {}},
		}},
		ChecksVersions: map[string]struct{}{"1": {}},
	}, trust
}
