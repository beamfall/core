package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/tools/wire-contract/wrapvectors"
)

const (
	offlinePackWrapVectorArtifact = "internal/conformance/testdata/offlinepack-wrap/v1.0.0/vectors.json"
	offlinePackWrapBoundary       = "offline-pack-wrap-vectors"
)

func TestOfflinePackWrapVectorCorpusFreshness(t *testing.T) {
	generated, err := wrapvectors.Generate()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repoRoot(t), filepath.FromSlash(offlinePackWrapVectorArtifact))
	if os.Getenv("BEAMFALL_UPDATE_WRAP_VECTORS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, generated, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	committed, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(offlinePackWrapVectorArtifact)))
	if err != nil {
		t.Fatalf("read %s: %v", offlinePackWrapVectorArtifact, err)
	}
	if string(committed) != string(generated) {
		t.Fatalf("%s is stale; regenerate with BEAMFALL_UPDATE_WRAP_VECTORS=1", offlinePackWrapVectorArtifact)
	}
	if err := checkOfflinePackWrapRegistryFreshness(
		committed,
		filepath.Join(repoRoot(t), "contracts/registry.json"),
	); err != nil {
		t.Fatal(err)
	}
}

func TestOfflinePackWrapVectorCorpusCoverageAndTupleRule(t *testing.T) {
	corpus := readOfflinePackWrapCorpus(t)
	if err := wrapvectors.ValidateCoverage(corpus); err != nil {
		t.Fatal(err)
	}
	if err := wrapvectors.ValidateTupleRule(corpus); err != nil {
		t.Fatal(err)
	}

	// RED proof: an accidental cross-vector tuple collision must be detected.
	bad := corpus
	bad.Vectors = append([]wrapvectors.Vector(nil), corpus.Vectors...)
	bad.Vectors[1].Transcripts = append([]wrapvectors.WrapTranscript(nil), bad.Vectors[1].Transcripts...)
	bad.Vectors[1].Transcripts[0] = bad.Vectors[0].Transcripts[0]
	if err := wrapvectors.ValidateTupleRule(bad); err == nil {
		t.Fatal("cross-vector tuple collision passed the generation rule")
	}
}

func TestOfflinePackWrapVectorW14ProductionRejection(t *testing.T) {
	corpus := readOfflinePackWrapCorpus(t)
	w14 := corpus.Vectors[13]
	if err := wrapvectors.ValidateProductionTupleRule(w14.Transcripts); err == nil {
		t.Fatal("production tuple admission accepted W14's colliding second wrap")
	}

	distinct := append([]wrapvectors.WrapTranscript(nil), w14.Transcripts...)
	distinct[1] = corpus.Vectors[14].Transcripts[0]
	if err := wrapvectors.ValidateProductionTupleRule(distinct); err != nil {
		t.Fatalf("production tuple admission rejected a distinct control: %v", err)
	}
}

func TestOfflinePackWrapVectorsW16ThroughW20ReconstructFromArtifact(t *testing.T) {
	corpus := readOfflinePackWrapCorpus(t)
	if err := wrapvectors.ValidateBFOPReconstruction(corpus); err != nil {
		t.Fatal(err)
	}

	// RED proof: reconstruction must not recover an input from producer code.
	bad := corpus
	bad.Vectors = append([]wrapvectors.Vector(nil), corpus.Vectors...)
	bad.Vectors[15].Fields = cloneStringMap(corpus.Vectors[15].Fields)
	delete(bad.Vectors[15].Fields, "manifest_sha256_hex")
	if err := wrapvectors.ValidateBFOPReconstruction(bad); err == nil {
		t.Fatal("W16 reconstructed without its published manifest input")
	}
}

func TestOfflinePackWrapVectorReferenceVerifier(t *testing.T) {
	encoded := readOfflinePackWrapCorpusBytes(t)
	if err := wrapvectors.Verify(encoded); err != nil {
		t.Fatal(err)
	}
	drifted := append([]byte(nil), encoded...)
	drifted[len(drifted)-2] ^= 1
	registryPath := filepath.Join(repoRoot(t), "contracts", "registry.json")
	if err := checkOfflinePackWrapRegistryFreshness(drifted, registryPath); err == nil {
		t.Fatal("drifted corpus matched the real registry pin")
	}
}

func TestOfflinePackWrapVectorRegistryFreshnessRedProof(t *testing.T) {
	artifact := readOfflinePackWrapCorpusBytes(t)
	pin := sha256.Sum256(artifact)
	registry := fmt.Sprintf(
		`{"boundaries":[{"boundary":%q,"artifact":%q,"artifactSHA256":%q,"version":%q,"activeArtifactVersion":%q}]}`,
		offlinePackWrapBoundary,
		offlinePackWrapVectorArtifact,
		hex.EncodeToString(pin[:]),
		wrapvectors.ArtifactVersion,
		wrapvectors.ArtifactVersion,
	)
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registryPath, []byte(registry), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkOfflinePackWrapRegistryFreshness(artifact, registryPath); err != nil {
		t.Fatalf("matching registry pin failed: %v", err)
	}
	drifted := append([]byte(nil), artifact...)
	drifted[len(drifted)-2] ^= 1
	if err := checkOfflinePackWrapRegistryFreshness(drifted, registryPath); err == nil {
		t.Fatal("drifted corpus matched the registry pin")
	}
}

func readOfflinePackWrapCorpus(t *testing.T) wrapvectors.Corpus {
	t.Helper()
	corpus, err := wrapvectors.Decode(readOfflinePackWrapCorpusBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

func readOfflinePackWrapCorpusBytes(t *testing.T) []byte {
	t.Helper()
	encoded, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(offlinePackWrapVectorArtifact)))
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func checkOfflinePackWrapRegistryFreshness(artifact []byte, registryPath string) error {
	//nolint:gosec // production path is fixed; RED proof supplies a t.TempDir path.
	encoded, err := os.ReadFile(registryPath)
	if err != nil {
		return fmt.Errorf("read contract registry: %w", err)
	}
	var registry struct {
		Boundaries []struct {
			Boundary              string `json:"boundary"`
			Artifact              string `json:"artifact"`
			ArtifactSHA256        string `json:"artifactSHA256"`
			Version               string `json:"version"`
			ActiveArtifactVersion string `json:"activeArtifactVersion"`
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(encoded, &registry); err != nil {
		return fmt.Errorf("decode contract registry: %w", err)
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary != offlinePackWrapBoundary {
			continue
		}
		if boundary.Artifact != offlinePackWrapVectorArtifact {
			return fmt.Errorf("%s artifact = %q, want %q", offlinePackWrapBoundary, boundary.Artifact, offlinePackWrapVectorArtifact)
		}
		if boundary.Version != wrapvectors.ArtifactVersion ||
			boundary.ActiveArtifactVersion != wrapvectors.ArtifactVersion {
			return fmt.Errorf("%s version pin is stale", offlinePackWrapBoundary)
		}
		sum := sha256.Sum256(artifact)
		got := hex.EncodeToString(sum[:])
		if got != boundary.ArtifactSHA256 {
			return fmt.Errorf("%s sha256 = %s, registry pin = %s", offlinePackWrapBoundary, got, boundary.ArtifactSHA256)
		}
		return nil
	}
	return fmt.Errorf("%s boundary missing from contract registry", offlinePackWrapBoundary)
}
