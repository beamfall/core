package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

const wireContractArtifactSHA256Path = "wire-contract-artifacts.sha256"
const wireContractArtifactFixtureRoot = "."

// TestWireContractArtifactChecksums pins the raw bytes of every published wire
// contract. Published versions are immutable; a new release adds a new artifact
// and checksum instead of changing an existing file.
func TestWireContractArtifactChecksums(t *testing.T) {
	expected, err := parseSHA256Manifest(wireContractArtifactSHA256Path)
	if err != nil {
		t.Fatalf("read %s: %v", wireContractArtifactSHA256Path, err)
	}
	artifactRoot, err := os.OpenRoot(wireContractArtifactFixtureRoot)
	if err != nil {
		t.Fatalf("open wire contract artifact fixture root: %v", err)
	}
	defer func() {
		if err := artifactRoot.Close(); err != nil {
			t.Errorf("close wire contract artifact fixture root: %v", err)
		}
	}()

	paths, err := filepath.Glob(filepath.Join(wireContractArtifactFixtureRoot, "wire-contract-v*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(expected) {
		t.Fatalf("%s pins %d artifacts, but %d published artifacts exist", wireContractArtifactSHA256Path, len(expected), len(paths))
	}

	for _, path := range paths {
		relativePath, err := filepath.Rel(wireContractArtifactFixtureRoot, path)
		if err != nil {
			t.Errorf("resolve published artifact path %q against fixture root: %v", path, err)
			continue
		}
		if !filepath.IsLocal(relativePath) {
			t.Errorf("published artifact path %q escapes fixture root", path)
			continue
		}
		name := filepath.Base(relativePath)
		want, ok := expected[name]
		if !ok {
			t.Errorf("%s has no checksum for published artifact %s", wireContractArtifactSHA256Path, name)
			continue
		}

		content, err := artifactRoot.ReadFile(relativePath)
		if err != nil {
			t.Errorf("read published artifact %s: %v", name, err)
			continue
		}
		sum := sha256.Sum256(content)
		got := hex.EncodeToString(sum[:])
		if got != want {
			t.Errorf("published artifact %s sha256 drifted: live %s, committed %s", name, got, want)
		}
	}
}
