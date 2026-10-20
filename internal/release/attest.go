package release

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

// AttestOptions drives Attest, the one-shot release-attestation step the
// `beamfall release attest` CLI wraps.
type AttestOptions struct {
	Dir                 string          // dist dir holding SHA256SUMS and the artifacts
	Version             string          // release version
	Channel             string          // "stable" or "beta"
	Commit              string          // source git commit (provenance material), optional
	Timestamp           string          // injected RFC-3339 time for SBOM/provenance
	InvocationID        string          // CI workflow-run identity
	AdditionalArtifacts []string        // pre-existing files in Dir to bind into the manifest
	SigningSeed         []byte          // 32-byte ed25519 seed; empty ⇒ unsigned (dev only)
	AllowUnsigned       bool            // permit an unsigned manifest (dev only)
	Modules             []*debug.Module // SBOM module set; nil ⇒ CurrentModules()
}

// Attest reads <Dir>/SHA256SUMS, writes a (signed) release-manifest.json, an
// SPDX SBOM, and a SLSA provenance attestation into Dir. It refuses to write an
// unsigned manifest unless AllowUnsigned is set. It returns the written file
// names (relative to Dir) for the caller to report.
func Attest(opts AttestOptions) ([]string, error) {
	arts, err := ParseSums(filepath.Join(opts.Dir, SumsFile))
	if err != nil {
		return nil, err
	}
	if len(opts.SigningSeed) == 0 && !opts.AllowUnsigned {
		return nil, fmt.Errorf("release: refusing to attest without a signing key (set BEAMFALL_RELEASE_SIGNING_KEY, or pass --allow-unsigned for a dev build)")
	}
	if len(opts.SigningSeed) > 0 && (opts.Version == "" || opts.Channel == "" || opts.Commit == "" || opts.Timestamp == "" || opts.InvocationID == "") {
		return nil, fmt.Errorf("release: signed attestations require version, channel, commit, timestamp, and invocation ID")
	}
	if err := os.Remove(filepath.Join(opts.Dir, ManifestFile)); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("release: remove stale %s: %w", ManifestFile, err)
	}

	mods := opts.Modules
	if mods == nil {
		var ok bool
		if mods, ok = CurrentModules(); !ok {
			return nil, fmt.Errorf("release: build info unavailable — cannot generate SBOM")
		}
	}

	if err := writeJSON(filepath.Join(opts.Dir, SBOMFile), BuildSBOM(opts.Version, opts.Timestamp, mods)); err != nil {
		return nil, err
	}
	for _, name := range append([]string{SumsFile, SBOMFile}, opts.AdditionalArtifacts...) {
		if name == ManifestFile || name == ProvenanceFile {
			return nil, fmt.Errorf("release: reserved additional artifact %q", name)
		}
		artifact, err := artifactFromFile(opts.Dir, name)
		if err != nil {
			return nil, err
		}
		arts, err = appendUniqueArtifact(arts, artifact)
		if err != nil {
			return nil, err
		}
	}
	if err := writeJSON(filepath.Join(opts.Dir, ProvenanceFile), BuildProvenance(opts.Version, opts.Channel, opts.Commit, opts.InvocationID, opts.Timestamp, arts)); err != nil {
		return nil, err
	}
	provenance, err := artifactFromFile(opts.Dir, ProvenanceFile)
	if err != nil {
		return nil, err
	}
	arts, err = appendUniqueArtifact(arts, provenance)
	if err != nil {
		return nil, err
	}

	m := Manifest{
		Version: opts.Version, Channel: opts.Channel, Commit: opts.Commit,
		Timestamp: opts.Timestamp, InvocationID: opts.InvocationID, Artifacts: arts,
	}
	if len(opts.SigningSeed) > 0 {
		m.Signature, err = Sign(opts.SigningSeed, m)
		if err != nil {
			return nil, err
		}
	}
	manifestBytes, err := m.Marshal()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(opts.Dir, ManifestFile), manifestBytes, 0o644); err != nil {
		return nil, fmt.Errorf("release: write %s: %w", ManifestFile, err)
	}

	written := []string{ManifestFile, SBOMFile, ProvenanceFile}
	return written, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("release: marshal %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("release: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// DecodeSeed parses a base64 (std) 32-byte ed25519 seed, the form the
// BEAMFALL_RELEASE_SIGNING_KEY secret carries. An empty input returns a nil
// seed and no error (the "no key set" case the caller handles).
func DecodeSeed(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, nil
	}
	seed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("release: signing key is not valid base64: %w", err)
	}
	if len(seed) != 32 {
		return nil, fmt.Errorf("release: signing key must decode to 32 bytes, got %d", len(seed))
	}
	return seed, nil
}
