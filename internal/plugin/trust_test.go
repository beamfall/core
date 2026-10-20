package plugin

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pluginTrustManifestSHA256 = "e2dd6bc0a650be8fc43d5e14986876b6b5a28ce7eaef3bf714e1ae71415cbd60"

type trustContractKeyset struct {
	Version    string            `json:"version"`
	Authors    map[string]string `json:"authors"`
	Registries map[string]string `json:"registries"`
}

type trustContractVectors struct {
	Version string                `json:"version"`
	Vectors []trustContractVector `json:"vectors"`
}

type trustContractVector struct {
	Name        string          `json:"name"`
	Artifact    string          `json:"artifact"`
	Envelope    json.RawMessage `json:"envelope"`
	Revocations struct {
		List struct {
			RevokedKeyIDs []string `json:"revoked_key_ids"`
		} `json:"list"`
	} `json:"revocations"`
	Expected string `json:"expected"`
}

func TestAssessPluginTrustProducerVectors(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	want := map[string]PluginTrustAssessment{
		"valid":                      trustAssessment(PluginTrusted),
		"artifact_hash_mismatch":     trustAssessment(PluginTampered),
		"author_signature_invalid":   trustAssessment(PluginTampered),
		"registry_signature_invalid": trustAssessment(PluginTampered),
		"author_root_unknown":        trustAssessment(PluginUnrecognized),
		"registry_root_unknown":      trustAssessment(PluginUnrecognized),
		"author_key_revoked":         trustAssessment(PluginRevoked),
	}
	for _, vector := range vectors.Vectors {
		vector := vector
		t.Run(vector.Name, func(t *testing.T) {
			artifact, err := base64.RawURLEncoding.DecodeString(vector.Artifact)
			if err != nil {
				t.Fatal(err)
			}
			got := AssessPluginTrust(artifact, vector.Envelope, roots, vector.Revocations.List.RevokedKeyIDs)
			if got != want[vector.Expected] {
				t.Fatalf("assessment = %#v, want %#v", got, want[vector.Expected])
			}
		})
	}
}

func TestAssessPluginTrustPrecedence(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	valid := findTrustVector(t, vectors, "valid")
	unknownAuthor := findTrustVector(t, vectors, "unknown-author-root")
	unknownRegistry := findTrustVector(t, vectors, "unknown-registry-root")
	validArtifact := decodeTrustArtifact(t, valid)

	tests := []struct {
		name     string
		artifact []byte
		envelope []byte
		roots    PluginTrustRoots
		revoked  []string
		want     PluginTrustAssessment
	}{
		{"unsigned", validArtifact, nil, roots, nil, trustAssessment(PluginUnsigned)},
		{"tampered over revoked", []byte("changed"), valid.Envelope, roots, []string{"author-fixture-1"}, trustAssessment(PluginTampered)},
		{"revoked over unknown author", decodeTrustArtifact(t, unknownAuthor), unknownAuthor.Envelope, roots, []string{"author-unknown-1"}, trustAssessment(PluginRevoked)},
		{"revoked over unknown registry", decodeTrustArtifact(t, unknownRegistry), unknownRegistry.Envelope, roots, []string{"author-fixture-1"}, trustAssessment(PluginRevoked)},
		{"unknown checks version", validArtifact, valid.Envelope, PluginTrustRoots{Authors: roots.Authors, Registries: roots.Registries}, nil, trustAssessment(PluginUnrecognized)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AssessPluginTrust(test.artifact, test.envelope, test.roots, test.revoked); got != test.want {
				t.Fatalf("assessment = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAssessPluginTrustRejectsMalformedEnvelope(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	valid := findTrustVector(t, vectors, "valid")
	artifact := decodeTrustArtifact(t, valid)
	compact := compactTrustJSON(t, valid.Envelope)

	tests := map[string][]byte{
		"empty":                     {},
		"whitespace":                []byte(" \n\t"),
		"null":                      []byte("null"),
		"wrong top-level type":      []byte("[]"),
		"trailing value":            append(append([]byte(nil), compact...), []byte("{}")...),
		"duplicate key":             []byte(strings.Replace(string(compact), `"registry_key_id":"registry-fixture-1"`, `"registry_key_id":"registry-fixture-1","registry_key_id":"registry-fixture-1"`, 1)),
		"case variant top key":      []byte(strings.Replace(string(compact), `"checks_version"`, `"CHECKS_VERSION"`, 1)),
		"case variant nested key":   []byte(strings.Replace(string(compact), `"author_signature"`, `"AUTHOR_SIGNATURE"`, 1)),
		"case variant manifest key": []byte(strings.Replace(string(compact), `"plugin_id"`, `"PLUGIN_ID"`, 1)),
		"semantic duplicate key":    []byte(strings.Replace(string(compact), `"checks_version":"1"`, `"checks_version":"1","CHECKS_VERSION":"1"`, 1)),
		"unknown top field":         mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["unknown"] = true }),
		"unknown nested field":      mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["author_signed"].(map[string]any)["unknown"] = true }),
		"unknown manifest field": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
			envelope["author_signed"].(map[string]any)["manifest"].(map[string]any)["unknown"] = true
		}),
		"missing author signature": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { delete(envelope["author_signed"].(map[string]any), "author_signature") }),
		"missing issued at": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
			delete(envelope["author_signed"].(map[string]any)["manifest"].(map[string]any), "issued_at")
		}),
		"empty registry key":       mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["registry_key_id"] = "" }),
		"empty checks version":     mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["checks_version"] = "" }),
		"bad author base64":        mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["author_signed"].(map[string]any)["author_signature"] = "!" }),
		"short registry signature": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) { envelope["registry_countersignature"] = "AA" }),
		"short hash": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
			envelope["author_signed"].(map[string]any)["manifest"].(map[string]any)["artifact_sha256"] = "00"
		}),
		"uppercase hash": mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
			manifest := envelope["author_signed"].(map[string]any)["manifest"].(map[string]any)
			manifest["artifact_sha256"] = strings.ToUpper(manifest["artifact_sha256"].(string))
		}),
	}
	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			if got := AssessPluginTrust(artifact, envelope, roots, nil); got != trustAssessment(PluginTampered) {
				t.Fatalf("assessment = %#v, want Tampered", got)
			}
		})
	}
}

func TestAssessPluginTrustUsesClaimedRootOnly(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	valid := findTrustVector(t, vectors, "valid")
	artifact := decodeTrustArtifact(t, valid)
	wrongPublic := bytes.Repeat([]byte{1}, ed25519.PublicKeySize)
	roots.Registries["registry-other-1"] = RegistryTrustRoot{
		PublicKey:      wrongPublic,
		ChecksVersions: map[string]struct{}{"1": {}},
	}
	envelope := mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
		envelope["registry_key_id"] = "registry-other-1"
	})
	if got := AssessPluginTrust(artifact, envelope, roots, nil); got != trustAssessment(PluginTampered) {
		t.Fatalf("assessment = %#v, want Tampered", got)
	}

	shortKeyRoots := contractTrustRoots(t, keys)
	shortKeyRoots.Authors["author-fixture-1"] = ed25519.PublicKey{1}
	if got := AssessPluginTrust(artifact, valid.Envelope, shortKeyRoots, nil); got != trustAssessment(PluginTampered) {
		t.Fatalf("short recognized key assessment = %#v, want Tampered", got)
	}
}

func TestAssessPluginTrustRejectsTamperedChecksVersion(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	roots.ChecksVersions["2"] = struct{}{}
	valid := findTrustVector(t, vectors, "valid")
	envelope := mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
		envelope["checks_version"] = "2"
	})

	got := AssessPluginTrust(decodeTrustArtifact(t, valid), envelope, roots, nil)
	if got != trustAssessment(PluginTampered) {
		t.Fatalf("assessment = %#v, want Tampered", got)
	}
}

func TestAssessPluginTrustRejectsAllowedToAllowedChecksVersionTampering(t *testing.T) {
	keys, vectors := loadTrustContract(t)
	roots := contractTrustRoots(t, keys)
	roots.ChecksVersions["2"] = struct{}{}
	registryRoot := roots.Registries["registry-fixture-1"]
	registryRoot.ChecksVersions = map[string]struct{}{"1": {}, "2": {}}
	roots.Registries["registry-fixture-1"] = registryRoot
	valid := findTrustVector(t, vectors, "valid")
	envelope := mutateTrustEnvelope(t, valid.Envelope, func(envelope map[string]any) {
		envelope["checks_version"] = "2"
	})

	got := AssessPluginTrust(decodeTrustArtifact(t, valid), envelope, roots, nil)
	if got != trustAssessment(PluginTampered) {
		t.Fatalf("assessment = %#v, want Tampered", got)
	}
}

func TestAssessArtifactTrustReusesOfflineSignatureChain(t *testing.T) {
	artifact := []byte("numeric model weights")
	digest := sha256.Sum256(artifact)
	authorPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	registryPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize))
	manifest, err := json.Marshal(map[string]any{
		"artifact_id":     "podcast-ad-audio-cpu",
		"artifact_sha256": hex.EncodeToString(digest[:]),
		"author_key_id":   "author-model-1",
		"version":         "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorSignature := base64.RawURLEncoding.EncodeToString(ed25519.Sign(authorPrivate, manifest))
	registryPayload, err := canonicalArtifactSigned(manifest, authorSignature)
	if err != nil {
		t.Fatal(err)
	}
	envelope := &ArtifactTrustEnvelope{
		ArtifactSHA256:           hex.EncodeToString(digest[:]),
		AuthorKeyID:              "author-model-1",
		AuthorSignature:          authorSignature,
		ChecksVersion:            "1",
		RegistryKeyID:            "registry-model-1",
		RegistryCountersignature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(registryPrivate, registryPayload)),
	}
	roots := PluginTrustRoots{
		Authors: map[string]ed25519.PublicKey{"author-model-1": authorPrivate.Public().(ed25519.PublicKey)},
		Registries: map[string]RegistryTrustRoot{"registry-model-1": {
			PublicKey:      registryPrivate.Public().(ed25519.PublicKey),
			ChecksVersions: map[string]struct{}{"1": {}},
		}},
		ChecksVersions: map[string]struct{}{"1": {}},
	}

	if got := AssessArtifactTrust(artifact, manifest, nil, roots, nil); got != trustAssessment(PluginUnsigned) {
		t.Fatalf("unsigned assessment = %#v", got)
	}
	if got := AssessArtifactTrust(artifact, manifest, envelope, roots, nil); got != trustAssessment(PluginTrusted) {
		t.Fatalf("trusted assessment = %#v", got)
	}
	tampered := *envelope
	tampered.AuthorSignature = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, ed25519.SignatureSize))
	if got := AssessArtifactTrust(artifact, manifest, &tampered, roots, nil); got != trustAssessment(PluginTampered) {
		t.Fatalf("tampered assessment = %#v", got)
	}
}

func TestPluginTrustContractPin(t *testing.T) {
	contractDir := filepath.Join("testdata", "plugin-trust", "v1")
	manifestRaw := readTrustFile(t, filepath.Join(contractDir, "manifest.json"))
	manifestDigest := sha256.Sum256(manifestRaw)
	if got := hex.EncodeToString(manifestDigest[:]); got != pluginTrustManifestSHA256 {
		t.Fatalf("manifest SHA-256 = %s, want %s", got, pluginTrustManifestSHA256)
	}
	var manifest struct {
		Version string            `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keyset.json", "schema.json", "vectors.json"} {
		digest := sha256.Sum256(readTrustFile(t, filepath.Join(contractDir, name)))
		if got := hex.EncodeToString(digest[:]); got != manifest.Files[name] {
			t.Fatalf("%s SHA-256 = %s, want %s", name, got, manifest.Files[name])
		}
	}

	var registry struct {
		Boundaries []struct {
			Boundary, Producer, Artifact, ArtifactSHA256, Version, ActiveArtifactVersion string
			Consumers                                                                    []json.RawMessage
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(readTrustFile(t, filepath.Join("..", "..", "contracts", "registry.json")), &registry); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary != "plugin-artifact-trust" {
			continue
		}
		if boundary.Producer != "beamfall-cloud" || boundary.Artifact != "internal/plugin/testdata/plugin-trust/v1" ||
			boundary.ArtifactSHA256 != pluginTrustManifestSHA256 || boundary.Version != "1" || boundary.ActiveArtifactVersion != "1" ||
			len(boundary.Consumers) != 1 || string(boundary.Consumers[0]) != `"beamfall"` {
			t.Fatalf("plugin trust registry boundary drift: %#v", boundary)
		}
		return
	}
	t.Fatal("plugin-artifact-trust missing from contracts/registry.json")
}

func loadTrustContract(t *testing.T) (trustContractKeyset, trustContractVectors) {
	t.Helper()
	var keys trustContractKeyset
	if err := json.Unmarshal(readTrustFile(t, filepath.Join("testdata", "plugin-trust", "v1", "keyset.json")), &keys); err != nil {
		t.Fatal(err)
	}
	var vectors trustContractVectors
	if err := json.Unmarshal(readTrustFile(t, filepath.Join("testdata", "plugin-trust", "v1", "vectors.json")), &vectors); err != nil {
		t.Fatal(err)
	}
	return keys, vectors
}

func contractTrustRoots(t *testing.T, keys trustContractKeyset) PluginTrustRoots {
	t.Helper()
	registries := make(map[string]RegistryTrustRoot, len(keys.Registries))
	for id, key := range decodeTrustKeys(t, keys.Registries) {
		registries[id] = RegistryTrustRoot{
			PublicKey:      key,
			ChecksVersions: map[string]struct{}{"1": {}},
		}
	}
	return PluginTrustRoots{
		Authors:        decodeTrustKeys(t, keys.Authors),
		Registries:     registries,
		ChecksVersions: map[string]struct{}{"1": {}},
	}
}

func decodeTrustKeys(t *testing.T, encoded map[string]string) map[string]ed25519.PublicKey {
	t.Helper()
	keys := make(map[string]ed25519.PublicKey, len(encoded))
	for id, value := range encoded {
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			t.Fatalf("invalid fixture public key %q", id)
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	return keys
}

func findTrustVector(t *testing.T, vectors trustContractVectors, name string) trustContractVector {
	t.Helper()
	for _, vector := range vectors.Vectors {
		if vector.Name == name {
			return vector
		}
	}
	t.Fatalf("missing vector %q", name)
	return trustContractVector{}
}

func decodeTrustArtifact(t *testing.T, vector trustContractVector) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(vector.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mutateTrustEnvelope(t *testing.T, raw []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	mutate(envelope)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func compactTrustJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		t.Fatal(err)
	}
	return compact.Bytes()
}

func readTrustFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- test paths are fixed contract fixtures.
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
