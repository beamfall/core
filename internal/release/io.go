package release

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadManifest reads and decodes a release-manifest.json. A missing or
// malformed manifest is an error the caller must treat as fail-closed.
func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("release: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("release: parse manifest: %w", err)
	}
	return m, nil
}

// Marshal renders m as pretty JSON with a trailing newline — the on-disk form
// of release-manifest.json. (Signing always uses SigningBytes, never this
// representation, so formatting here is cosmetic.)
func (m Manifest) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(m.withSortedArtifacts(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("release: marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}

// withSortedArtifacts returns m with Artifacts sorted (and Signature retained),
// so the written file matches the canonical artifact order used for signing.
func (m Manifest) withSortedArtifacts() Manifest {
	out := m.normalized()
	out.Signature = m.Signature
	return out
}
