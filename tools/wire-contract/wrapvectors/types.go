// Package wrapvectors builds and verifies the producer-owned ADR-0208 W-series
// conformance artifact. It is deliberately independent of internal/offlinepack:
// the reference verifier must remain a useful tiebreak when implementations
// disagree.
package wrapvectors

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const (
	ArtifactName    = "beamfall.offline-pack-wrap-vectors"
	ArtifactVersion = "1.0.0"
	VectorCount     = 28
)

type Corpus struct {
	SchemaVersion   int      `json:"schema_version"`
	Artifact        string   `json:"artifact"`
	ArtifactVersion string   `json:"artifact_version"`
	Contract        string   `json:"contract"`
	Vectors         []Vector `json:"vectors"`
}

type Vector struct {
	ID          string              `json:"id"`
	Description string              `json:"description"`
	Expected    string              `json:"expected"`
	Transcripts []WrapTranscript    `json:"wrap_transcripts"`
	Fields      map[string]string   `json:"fields,omitempty"`
	Lists       map[string][]string `json:"lists,omitempty"`
}

type WrapTranscript struct {
	DevicePrivateKeyHex    string `json:"device_private_key_hex"`
	DevicePublicKeyHex     string `json:"device_public_key_hex"`
	EphemeralPrivateKeyHex string `json:"ephemeral_private_key_hex"`
	EphemeralPublicKeyHex  string `json:"ephemeral_public_key_hex"`
	AADWrapHex             string `json:"aad_wrap_hex"`
	CEKHex                 string `json:"cek_hex"`
	EnvelopeHex            string `json:"envelope_hex"`
}

func Encode(corpus Corpus) ([]byte, error) {
	encoded, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func Decode(encoded []byte) (Corpus, error) {
	var corpus Corpus
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode wrap-vector corpus: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Corpus{}, fmt.Errorf("decode wrap-vector corpus: trailing JSON")
	}
	return corpus, nil
}

func ValidateCoverage(corpus Corpus) error {
	if corpus.SchemaVersion != 1 {
		return fmt.Errorf("schema_version = %d, want 1", corpus.SchemaVersion)
	}
	if corpus.Artifact != ArtifactName {
		return fmt.Errorf("artifact = %q, want %q", corpus.Artifact, ArtifactName)
	}
	if corpus.ArtifactVersion != ArtifactVersion {
		return fmt.Errorf("artifact_version = %q, want %q", corpus.ArtifactVersion, ArtifactVersion)
	}
	if corpus.Contract != "ADR-0208" {
		return fmt.Errorf("contract = %q, want ADR-0208", corpus.Contract)
	}
	if len(corpus.Vectors) != VectorCount {
		return fmt.Errorf("vector count = %d, want %d", len(corpus.Vectors), VectorCount)
	}
	for index, vector := range corpus.Vectors {
		wantID := fmt.Sprintf("W%02d", index+1)
		if vector.ID != wantID {
			return fmt.Errorf("vector %d id = %q, want %q", index, vector.ID, wantID)
		}
		if vector.Description == "" || (vector.Expected != "accept" && vector.Expected != "reject") {
			return fmt.Errorf("%s has incomplete semantics", vector.ID)
		}
		wantTranscripts := 1
		if vector.ID == "W14" {
			wantTranscripts = 2
		}
		if len(vector.Transcripts) != wantTranscripts {
			return fmt.Errorf("%s transcript count = %d, want %d", vector.ID, len(vector.Transcripts), wantTranscripts)
		}
		for _, transcript := range vector.Transcripts {
			for name, value := range map[string]string{
				"device_private_key_hex":    transcript.DevicePrivateKeyHex,
				"device_public_key_hex":     transcript.DevicePublicKeyHex,
				"ephemeral_private_key_hex": transcript.EphemeralPrivateKeyHex,
				"ephemeral_public_key_hex":  transcript.EphemeralPublicKeyHex,
				"aad_wrap_hex":              transcript.AADWrapHex,
				"cek_hex":                   transcript.CEKHex,
				"envelope_hex":              transcript.EnvelopeHex,
			} {
				if value == "" {
					return fmt.Errorf("%s %s is empty", vector.ID, name)
				}
				if _, err := hex.DecodeString(value); err != nil {
					return fmt.Errorf("%s %s: %w", vector.ID, name, err)
				}
			}
		}
	}
	return nil
}

func ValidateTupleRule(corpus Corpus) error {
	seen := make(map[string]string, VectorCount)
	for _, vector := range corpus.Vectors {
		for _, transcript := range vector.Transcripts {
			tuple := tupleKey(transcript)
			first, exists := seen[tuple]
			if !exists {
				seen[tuple] = vector.ID
				continue
			}
			if first != "W14" || vector.ID != "W14" {
				return fmt.Errorf("tuple collision between %s and %s", first, vector.ID)
			}
		}
	}
	if len(corpus.Vectors) > 0 {
		w14 := corpus.Vectors[13]
		if len(w14.Transcripts) != 2 || tupleKey(w14.Transcripts[0]) != tupleKey(w14.Transcripts[1]) {
			return fmt.Errorf("W14 does not contain its deliberate repeated tuple")
		}
	}
	return nil
}

// ValidateProductionTupleRule applies the production admission rule: every
// wrap tuple must be one-use. W14 deliberately supplies the sole rejected pair.
func ValidateProductionTupleRule(transcripts []WrapTranscript) error {
	seen := make(map[string]struct{}, len(transcripts))
	for _, transcript := range transcripts {
		tuple := tupleKey(transcript)
		if _, exists := seen[tuple]; exists {
			return fmt.Errorf("production rejects repeated wrap tuple")
		}
		seen[tuple] = struct{}{}
	}
	return nil
}

func tupleKey(transcript WrapTranscript) string {
	return transcript.EphemeralPublicKeyHex + "\x00" +
		transcript.DevicePublicKeyHex + "\x00" + transcript.AADWrapHex
}
