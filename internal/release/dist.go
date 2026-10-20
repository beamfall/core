package release

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SHA256SUMS / manifest / sbom / provenance file names written into a dist dir.
const (
	SumsFile       = "SHA256SUMS"
	ManifestFile   = "release-manifest.json"
	SBOMFile       = "sbom.spdx.json"
	ProvenanceFile = "provenance.slsa.json"
)

// ParseSums reads a `shasum -a 256`-format SHA256SUMS file ("<hex>  <name>"
// per line, two spaces, an optional leading "./" on the name) into sorted
// Artifacts. It is the bridge between release.sh's checksum step and the signed
// manifest.
func ParseSums(path string) ([]Artifact, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("release: open %s: %w", filepath.Base(path), err)
	}
	defer f.Close()

	var arts []Artifact
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// "<64-hex><space><space|*><name>" — split on whitespace, the digest is
		// the first field and the name is the remainder (trimmed of a binary "*"
		// or "./" prefix).
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("release: malformed SHA256SUMS line %q", line)
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		name = strings.TrimPrefix(name, "./")
		artifact := Artifact{Name: name, SHA256: strings.ToLower(fields[0])}
		if err := validateArtifact(artifact); err != nil {
			return nil, err
		}
		arts = append(arts, artifact)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("release: read SHA256SUMS: %w", err)
	}
	if len(arts) == 0 {
		return nil, fmt.Errorf("release: SHA256SUMS is empty")
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	return arts, nil
}

// VerifyDist is the upgrade-path / CI gate over a built dist directory: it loads
// release-manifest.json, verifies its signature against the pinned trust anchor,
// then recomputes every listed artifact's SHA-256 from disk and refuses on any
// mismatch (a tampered artifact). A non-nil error means "do not apply".
func VerifyDist(dir string) error {
	return verifyDist(dir, VerifyWithPinnedKey)
}

// verifyDist is the testable core of VerifyDist: the signature gate is injected
// so the CI gate can exercise the full load → verify → recompute-from-disk →
// reject-tampered path with a test key (the pinned private seed is a release
// secret, never in the repo).
func verifyDist(dir string, verify func(Manifest) error) error {
	m, err := LoadManifest(filepath.Join(dir, ManifestFile))
	if err != nil {
		return err
	}
	if err := verify(m); err != nil {
		return err
	}
	for _, a := range m.Artifacts {
		if err := validateArtifact(a); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(dir, a.Name))
		if err != nil {
			return fmt.Errorf("release: refused — cannot read signed artifact %s: %w", a.Name, err)
		}
		if err := VerifyArtifact(m, a.Name, SHA256Hex(b)); err != nil {
			return err
		}
	}
	listed := make(map[string]struct{}, len(m.Artifacts))
	for _, artifact := range m.Artifacts {
		listed[filepath.Clean(artifact.Name)] = struct{}{}
	}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == ManifestFile {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release: refused unlisted non-regular file %s", rel)
		}
		if _, ok := listed[filepath.Clean(rel)]; !ok {
			return fmt.Errorf("release: refused unlisted artifact %s", rel)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func validateArtifact(artifact Artifact) error {
	if artifact.Name == "" || !filepath.IsLocal(artifact.Name) {
		return fmt.Errorf("release: refused unsafe artifact name %q", artifact.Name)
	}
	if len(artifact.SHA256) != sha256.Size*2 {
		return fmt.Errorf("release: refused invalid SHA-256 for artifact %q", artifact.Name)
	}
	if _, err := hex.DecodeString(artifact.SHA256); err != nil {
		return fmt.Errorf("release: refused invalid SHA-256 for artifact %q", artifact.Name)
	}
	return nil
}

func artifactFromFile(dir, name string) (Artifact, error) {
	artifact := Artifact{Name: name}
	if artifact.Name == "" || !filepath.IsLocal(artifact.Name) {
		return Artifact{}, fmt.Errorf("release: refused unsafe artifact name %q", artifact.Name)
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("release: read artifact %s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("release: refused non-regular artifact %q", name)
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Artifact{}, fmt.Errorf("release: resolve dist directory: %w", err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Artifact{}, fmt.Errorf("release: resolve artifact %s: %w", name, err)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil || !filepath.IsLocal(rel) {
		return Artifact{}, fmt.Errorf("release: refused artifact outside dist %q", name)
	}
	b, err := os.ReadFile(realPath)
	if err != nil {
		return Artifact{}, fmt.Errorf("release: read artifact %s: %w", name, err)
	}
	artifact.SHA256 = SHA256Hex(b)
	return artifact, validateArtifact(artifact)
}

func appendUniqueArtifact(arts []Artifact, artifact Artifact) ([]Artifact, error) {
	for _, existing := range arts {
		if existing.Name == artifact.Name {
			return nil, fmt.Errorf("release: duplicate artifact %q", artifact.Name)
		}
	}
	return append(arts, artifact), nil
}
