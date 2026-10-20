// Package release is the server's supply-chain integrity core (PANEL-11,
// ADR-0013 Amendment §A). It defines the signed release Manifest, the ed25519
// sign/verify primitives, the pinned trust-anchor public key, and the SBOM /
// build-provenance builders.
//
// The trust model is cosign/sigstore-style KEY PINNING: the public key is the
// committed trust anchor (key.go); the private seed is a release secret
// (BEAMFALL_RELEASE_SIGNING_KEY), never committed. Verification is FAIL-CLOSED
// — an empty/unset pinned key, a missing signature, a bad signature, or a
// digest mismatch all refuse. This is what lets the blessed unattended-upgrade
// path reject an unsigned or tampered server artifact before
// applying it.
//
// Leaf package: stdlib only, no internal imports (arch §4).
package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Artifact is one signed release file: its name and lowercase-hex SHA-256.
type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Manifest is the signed release manifest the release pipeline emits and the
// upgrade path verifies. Signature is a base64 (std) ed25519 signature over the
// canonical SigningBytes (which deliberately exclude Signature itself), so the
// document carries its own detached signature in one file / one GET.
type Manifest struct {
	Version      string     `json:"version"`
	Channel      string     `json:"channel"`
	Commit       string     `json:"commit,omitempty"`
	Timestamp    string     `json:"timestamp,omitempty"`
	InvocationID string     `json:"invocationId,omitempty"`
	Artifacts    []Artifact `json:"artifacts"`
	Signature    string     `json:"signature,omitempty"`
}

// normalized returns a copy with Artifacts sorted by name and Signature
// cleared, so signing and verification operate on a deterministic byte stream
// regardless of artifact enumeration order.
func (m Manifest) normalized() Manifest {
	arts := make([]Artifact, len(m.Artifacts))
	copy(arts, m.Artifacts)
	sort.Slice(arts, func(i, j int) bool { return arts[i].Name < arts[j].Name })
	return Manifest{
		Version:      m.Version,
		Channel:      m.Channel,
		Commit:       m.Commit,
		Timestamp:    m.Timestamp,
		InvocationID: m.InvocationID,
		Artifacts:    arts,
	}
}

// SigningBytes is the canonical, signature-excluded encoding that Sign signs and
// Verify checks. Structs (not maps) plus the sorted Artifacts give a stable,
// reproducible byte stream across encoders.
func (m Manifest) SigningBytes() ([]byte, error) {
	b, err := json.Marshal(m.normalized())
	if err != nil {
		return nil, fmt.Errorf("release: marshal signing bytes: %w", err)
	}
	return b, nil
}

// Sign returns the base64 (std) ed25519 signature of m's SigningBytes using the
// 32-byte private seed. The returned string is what callers store in
// Manifest.Signature.
func Sign(seed []byte, m Manifest) (string, error) {
	if len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("release: signing seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	msg, err := m.SigningBytes()
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), msg)
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks m.Signature against pub over m's SigningBytes. It is the single
// fail-closed gate: an empty pub, an empty/garbled signature, or a payload that
// does not match all refuse with a non-nil error.
func Verify(pub ed25519.PublicKey, m Manifest) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("release: refused — no usable verification key")
	}
	if strings.TrimSpace(m.Signature) == "" {
		return fmt.Errorf("release: refused — manifest is unsigned")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("release: refused — signature is not valid base64: %w", err)
	}
	msg, err := m.SigningBytes()
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, msg, sig) {
		return fmt.Errorf("release: refused — signature does not verify against the pinned key")
	}
	return nil
}

// VerifyArtifact refuses unless name is listed in m and its recorded digest
// equals gotSHA256 (case-insensitive hex). A missing artifact or a digest
// mismatch is a tampered/unknown artifact — the "reject tampered" primitive the
// upgrade path relies on. Callers must Verify(m) first; this only checks the
// digest binding, not the signature.
func VerifyArtifact(m Manifest, name, gotSHA256 string) error {
	got := strings.ToLower(strings.TrimSpace(gotSHA256))
	for _, a := range m.Artifacts {
		if a.Name == name {
			if strings.ToLower(strings.TrimSpace(a.SHA256)) != got {
				return fmt.Errorf("release: refused — %s digest mismatch (tampered): manifest %s, got %s", name, a.SHA256, got)
			}
			return nil
		}
	}
	return fmt.Errorf("release: refused — %s is not in the signed manifest", name)
}

// SHA256Hex is the lowercase-hex SHA-256 of b — the digest form used throughout
// the manifest and SHA256SUMS.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
