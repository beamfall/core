package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// PluginTrustState is advisory integrity standing. It never decides whether a
// plugin loads.
type PluginTrustState string

const (
	PluginTrusted      PluginTrustState = "Trusted"
	PluginTampered     PluginTrustState = "Tampered"
	PluginRevoked      PluginTrustState = "Revoked"
	PluginUnrecognized PluginTrustState = "Unrecognized"
	PluginUnsigned     PluginTrustState = "Unsigned"
)

const (
	DiagnosticPluginTrusted      = "plugin_trust_trusted"
	DiagnosticPluginTampered     = "plugin_trust_tampered"
	DiagnosticPluginRevoked      = "plugin_trust_revoked"
	DiagnosticPluginUnrecognized = "plugin_trust_unrecognized"
	DiagnosticPluginUnsigned     = "plugin_trust_unsigned"
)

type PluginTrustAssessment struct {
	State      PluginTrustState
	Diagnostic string
}

// PluginTrustRoots is the release-bundled public trust material. Keys are
// selected only by the IDs claimed in the signed envelope.
type PluginTrustRoots struct {
	Authors        map[string]ed25519.PublicKey
	Registries     map[string]RegistryTrustRoot
	ChecksVersions map[string]struct{}
	// FirstPartyAuthorKeyIDs classifies authenticated authorship for display;
	// provenance remains orthogonal to capability and never authorizes behavior.
	FirstPartyAuthorKeyIDs map[string]struct{}
}

// RegistryTrustRoot binds a registry identity and its accepted checks
// versions to one release-bundled verification key.
type RegistryTrustRoot struct {
	PublicKey      ed25519.PublicKey
	ChecksVersions map[string]struct{}
}

// ArtifactTrustEnvelope is the signature material shared by governed
// artifacts. CanonicalManifest must include ArtifactSHA256 and AuthorKeyID.
type ArtifactTrustEnvelope struct {
	ArtifactSHA256           string
	AuthorKeyID              string
	AuthorSignature          string
	ChecksVersion            string
	RegistryKeyID            string
	RegistryCountersignature string
}

type artifactManifest struct {
	PluginID       string    `json:"plugin_id"`
	Version        string    `json:"version"`
	ArtifactSHA256 string    `json:"artifact_sha256"`
	AuthorKeyID    string    `json:"author_key_id"`
	IssuedAt       time.Time `json:"issued_at"`
}

type authorSignedArtifact struct {
	Manifest        artifactManifest `json:"manifest"`
	AuthorSignature string           `json:"author_signature"`
}

type countersignedArtifact struct {
	AuthorSigned             authorSignedArtifact `json:"author_signed"`
	ChecksVersion            string               `json:"checks_version"`
	RegistryKeyID            string               `json:"registry_key_id"`
	RegistryCountersignature string               `json:"registry_countersignature"`
}

// AssessPluginTrust verifies cached bytes without network, filesystem,
// persistence, event, or loader effects. A nil envelope means no signature was
// supplied; every non-nil malformed envelope is Tampered.
func AssessPluginTrust(artifact, envelopeJSON []byte, roots PluginTrustRoots, revokedAuthorKeyIDs []string) PluginTrustAssessment {
	if envelopeJSON == nil {
		return trustAssessment(PluginUnsigned)
	}

	var envelope countersignedArtifact
	if decodeTrustEnvelope(envelopeJSON, &envelope) != nil || !validTrustEnvelope(envelope) {
		return trustAssessment(PluginTampered)
	}
	manifest, err := canonicalManifest(envelope.AuthorSigned.Manifest)
	if err != nil {
		return trustAssessment(PluginTampered)
	}
	return AssessArtifactTrust(artifact, manifest, &ArtifactTrustEnvelope{
		ArtifactSHA256:           envelope.AuthorSigned.Manifest.ArtifactSHA256,
		AuthorKeyID:              envelope.AuthorSigned.Manifest.AuthorKeyID,
		AuthorSignature:          envelope.AuthorSigned.AuthorSignature,
		ChecksVersion:            envelope.ChecksVersion,
		RegistryKeyID:            envelope.RegistryKeyID,
		RegistryCountersignature: envelope.RegistryCountersignature,
	}, roots, revokedAuthorKeyIDs)
}

// AssessArtifactTrust evaluates a canonical governed-artifact manifest using
// the same offline author/registry Ed25519 chain as plugin artifacts. Trust is
// advisory: callers separately enforce format and compatibility safety.
func AssessArtifactTrust(
	artifact, canonicalManifest []byte,
	envelope *ArtifactTrustEnvelope,
	roots PluginTrustRoots,
	revokedAuthorKeyIDs []string,
) PluginTrustAssessment {
	if envelope == nil {
		return trustAssessment(PluginUnsigned)
	}
	if !validArtifactTrustEnvelope(*envelope) {
		return trustAssessment(PluginTampered)
	}
	digest := sha256.Sum256(artifact)
	if envelope.ArtifactSHA256 != hex.EncodeToString(digest[:]) {
		return trustAssessment(PluginTampered)
	}

	authorSignature, err := base64.RawURLEncoding.DecodeString(envelope.AuthorSignature)
	if err != nil || len(authorSignature) != ed25519.SignatureSize {
		return trustAssessment(PluginTampered)
	}
	registrySignature, err := base64.RawURLEncoding.DecodeString(envelope.RegistryCountersignature)
	if err != nil || len(registrySignature) != ed25519.SignatureSize {
		return trustAssessment(PluginTampered)
	}

	authorPublic, authorKnown := roots.Authors[envelope.AuthorKeyID]
	if authorKnown && (len(authorPublic) != ed25519.PublicKeySize || !ed25519.Verify(authorPublic, canonicalManifest, authorSignature)) {
		return trustAssessment(PluginTampered)
	}
	registryRoot, registryKnown := roots.Registries[envelope.RegistryKeyID]
	_, checksKnown := roots.ChecksVersions[envelope.ChecksVersion]
	if registryKnown {
		// The v1 countersignature contract covers author_signed but not
		// checks_version, so each trusted registry root must bind exactly one
		// checks version to prevent allowed-to-allowed version substitution.
		if len(registryRoot.ChecksVersions) != 1 {
			return trustAssessment(PluginTampered)
		}
		if checksKnown {
			if _, bound := registryRoot.ChecksVersions[envelope.ChecksVersion]; !bound {
				return trustAssessment(PluginTampered)
			}
		}
		payload, err := canonicalArtifactSigned(canonicalManifest, envelope.AuthorSignature)
		if err != nil || len(registryRoot.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(registryRoot.PublicKey, payload, registrySignature) {
			return trustAssessment(PluginTampered)
		}
	}

	if containsTrustKeyID(revokedAuthorKeyIDs, envelope.AuthorKeyID) {
		return trustAssessment(PluginRevoked)
	}
	if !authorKnown || !registryKnown || !checksKnown {
		return trustAssessment(PluginUnrecognized)
	}
	return trustAssessment(PluginTrusted)
}

func trustAssessment(state PluginTrustState) PluginTrustAssessment {
	var diagnostic string
	switch state {
	case PluginTrusted:
		diagnostic = DiagnosticPluginTrusted
	case PluginTampered:
		diagnostic = DiagnosticPluginTampered
	case PluginRevoked:
		diagnostic = DiagnosticPluginRevoked
	case PluginUnrecognized:
		diagnostic = DiagnosticPluginUnrecognized
	case PluginUnsigned:
		diagnostic = DiagnosticPluginUnsigned
	}
	return PluginTrustAssessment{State: state, Diagnostic: diagnostic}
}

func validTrustEnvelope(envelope countersignedArtifact) bool {
	manifest := envelope.AuthorSigned.Manifest
	if manifest.PluginID == "" || manifest.Version == "" || manifest.AuthorKeyID == "" || manifest.IssuedAt.IsZero() ||
		envelope.AuthorSigned.AuthorSignature == "" || envelope.ChecksVersion == "" || envelope.RegistryKeyID == "" || envelope.RegistryCountersignature == "" {
		return false
	}
	return validArtifactHash(manifest.ArtifactSHA256)
}

func validArtifactTrustEnvelope(envelope ArtifactTrustEnvelope) bool {
	if envelope.AuthorKeyID == "" || envelope.AuthorSignature == "" || envelope.ChecksVersion == "" ||
		envelope.RegistryKeyID == "" || envelope.RegistryCountersignature == "" {
		return false
	}
	return validArtifactHash(envelope.ArtifactSHA256)
}

func validArtifactHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func decodeTrustEnvelope(raw []byte, envelope *countersignedArtifact) error {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}
	if err := requireExactTrustEnvelopeKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(envelope); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("plugin trust envelope has trailing JSON")
	}
	return nil
}

func requireExactTrustEnvelopeKeys(raw []byte) error {
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		return err
	}
	if !hasExactJSONKeys(outer, "author_signed", "checks_version", "registry_key_id", "registry_countersignature") {
		return errors.New("plugin trust envelope has invalid keys")
	}

	var signed map[string]json.RawMessage
	if err := json.Unmarshal(outer["author_signed"], &signed); err != nil {
		return err
	}
	if !hasExactJSONKeys(signed, "manifest", "author_signature") {
		return errors.New("plugin trust author envelope has invalid keys")
	}

	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(signed["manifest"], &manifest); err != nil {
		return err
	}
	if !hasExactJSONKeys(manifest, "plugin_id", "version", "artifact_sha256", "author_key_id", "issued_at") {
		return errors.New("plugin trust manifest has invalid keys")
	}
	return nil
}

func hasExactJSONKeys(object map[string]json.RawMessage, keys ...string) bool {
	if len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	return scanJSONValue(decoder)
}

// RejectDuplicateJSONKeys rejects duplicate object members at every nesting
// level. Strict artifact decoders use it before typed JSON decoding.
func RejectDuplicateJSONKeys(raw []byte) error {
	return rejectDuplicateJSONKeys(raw)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("plugin trust envelope has a non-string object key")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("plugin JSON has duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("plugin trust envelope has invalid JSON")
	}
	_, err = decoder.Token()
	return err
}

func canonicalManifest(manifest artifactManifest) ([]byte, error) {
	return json.Marshal(map[string]any{
		"artifact_sha256": manifest.ArtifactSHA256,
		"author_key_id":   manifest.AuthorKeyID,
		"issued_at":       manifest.IssuedAt,
		"plugin_id":       manifest.PluginID,
		"version":         manifest.Version,
	})
}

func canonicalAuthorSigned(signed authorSignedArtifact) ([]byte, error) {
	manifest, err := canonicalManifest(signed.Manifest)
	if err != nil {
		return nil, err
	}
	return canonicalArtifactSigned(manifest, signed.AuthorSignature)
}

func canonicalArtifactSigned(manifest []byte, authorSignature string) ([]byte, error) {
	var genericManifest any
	if err := json.Unmarshal(manifest, &genericManifest); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"author_signature": authorSignature,
		"manifest":         genericManifest,
	})
}

func containsTrustKeyID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
