package capabilities

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// FirstPartyBuildManifest is the release-supplied record of the first-party
// native client builds this Core ships alongside (ADR-0063 build provenance).
// The release process sets it at link time, like ReleaseVersion:
//
//	-X github.com/beamfall/core/internal/capabilities.FirstPartyBuildManifest=<base64 manifest>
//
// script/release.sh produces the value with `beamfall release build-catalog`,
// which validates the manifest before it is linked. Empty means the binary
// carries no first-party native builds, so every native build is unknown and
// refused; only the embedded Core Web UI remains known. Operator rollout policy
// still arrives through Server.SetCompatPolicy.
var FirstPartyBuildManifest string

// FirstPartyBuildManifestSchema names the only accepted manifest format.
const FirstPartyBuildManifestSchema = "beamfall.first-party-builds/v1"

// maxFirstPartyBuildIDLength bounds a manifest build id, matching the
// durable enrollment binding's column check.
const maxFirstPartyBuildIDLength = 256

type firstPartyBuildManifest struct {
	Schema          string            `json:"schema"`
	Builds          []firstPartyBuild `json:"builds"`
	MinimumSequence map[Surface]int   `json:"minimumSequence,omitempty"`
}

type firstPartyBuild struct {
	BuildID       string           `json:"buildID"`
	Surface       Surface          `json:"surface"`
	ArtifactHash  string           `json:"artifactHash"`
	Sequence      int              `json:"sequence"`
	Distributions []Distribution   `json:"distributions"`
	Roles         []EnrollmentRole `json:"roles"`
}

// FirstPartyBuildCatalog decodes the link-time FirstPartyBuildManifest into the
// catalog a production server installs at boot. It carries build records and
// rollback floors only: the exact enrollment mapping comes from server-held
// enrollment state, never from a release manifest.
func FirstPartyBuildCatalog() (BuildCatalog, error) {
	encoded := strings.TrimSpace(FirstPartyBuildManifest)
	if encoded == "" {
		return BuildCatalog{}, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return BuildCatalog{}, fmt.Errorf("first-party build manifest: invalid base64: %w", err)
	}
	return ParseFirstPartyBuildManifest(data)
}

// EncodeFirstPartyBuildManifest validates a manifest and returns the link-time
// value for FirstPartyBuildManifest.
func EncodeFirstPartyBuildManifest(data []byte) (string, error) {
	if _, err := ParseFirstPartyBuildManifest(data); err != nil {
		return "", err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return "", fmt.Errorf("first-party build manifest: %w", err)
	}
	return base64.StdEncoding.EncodeToString(compact.Bytes()), nil
}

// ParseFirstPartyBuildManifest strictly decodes a first-party build manifest.
// It refuses rather than drops anything it cannot vouch for: an unknown field
// or schema, a non-native surface, a malformed digest, a non-positive release
// ordinal, an empty or unknown distribution or role, and a duplicated build id.
func ParseFirstPartyBuildManifest(data []byte) (BuildCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest firstPartyBuildManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BuildCatalog{}, fmt.Errorf("first-party build manifest: %w", err)
	}
	// Only whitespace may follow the manifest. More reports false for a stray
	// closing bracket, so require the decoder to reach EOF instead.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return BuildCatalog{}, errors.New("first-party build manifest: trailing data")
	}
	if manifest.Schema != FirstPartyBuildManifestSchema {
		return BuildCatalog{}, fmt.Errorf("first-party build manifest: schema %q, want %q", manifest.Schema, FirstPartyBuildManifestSchema)
	}
	catalog := BuildCatalog{
		Builds:          make([]BuildRecord, 0, len(manifest.Builds)),
		MinimumSequence: make(map[Surface]int, len(manifest.MinimumSequence)),
	}
	seen := make(map[string]struct{}, len(manifest.Builds))
	for i, build := range manifest.Builds {
		record := BuildRecord{
			BuildID:       build.BuildID,
			Surface:       build.Surface,
			ArtifactHash:  build.ArtifactHash,
			Sequence:      build.Sequence,
			Distributions: append([]Distribution(nil), build.Distributions...),
			Roles:         append([]EnrollmentRole(nil), build.Roles...),
		}
		if err := validateFirstPartyBuild(record); err != nil {
			return BuildCatalog{}, fmt.Errorf("first-party build manifest: builds[%d]: %w", i, err)
		}
		if _, dup := seen[record.BuildID]; dup {
			return BuildCatalog{}, fmt.Errorf("first-party build manifest: builds[%d]: duplicate buildID %q", i, record.BuildID)
		}
		seen[record.BuildID] = struct{}{}
		catalog.Builds = append(catalog.Builds, record)
	}
	for surface, floor := range manifest.MinimumSequence {
		if !IsNativeSurface(surface) {
			return BuildCatalog{}, fmt.Errorf("first-party build manifest: minimumSequence: %q is not a first-party native surface", surface)
		}
		if floor < 0 {
			return BuildCatalog{}, fmt.Errorf("first-party build manifest: minimumSequence[%s] is negative", surface)
		}
		catalog.MinimumSequence[surface] = floor
	}
	return catalog, nil
}

func validateFirstPartyBuild(record BuildRecord) error {
	if record.BuildID == "" || record.BuildID != strings.TrimSpace(record.BuildID) || len(record.BuildID) > maxFirstPartyBuildIDLength {
		return fmt.Errorf("buildID %q must be 1-%d characters without surrounding space", record.BuildID, maxFirstPartyBuildIDLength)
	}
	if !IsNativeSurface(record.Surface) {
		return fmt.Errorf("surface %q is not a first-party native surface", record.Surface)
	}
	if !validArtifactHash(record.ArtifactHash) {
		return errors.New("artifactHash must be a lowercase hex SHA-256 digest")
	}
	if !buildRecordUsable(record) {
		return errors.New("record needs a positive sequence and known, non-empty distributions and roles")
	}
	return nil
}

// EnrollmentBindingFor returns the exact enrollment-to-build mapping an
// enrollment may record for contract. It runs the unchanged
// VerifyBuildProvenance against the catalog plus that one candidate mapping, so
// every other check (known usable record, surface, digest, rollback floor, role
// and distribution) must already pass; the only thing supplied is the mapping
// this enrollment is creating. It never grants a mapping to another enrollment.
// A finding other than ProvenanceVerified means no mapping may be recorded.
func EnrollmentBindingFor(contract ClientContract, enrollment Enrollment, catalog BuildCatalog) (EnrollmentBuildBinding, ProvenanceFinding) {
	contract = NormalizeClientContract(contract)
	binding := EnrollmentBuildBinding{
		EnrollmentID: strings.TrimSpace(enrollment.ID),
		Surface:      Surface(contract.Surface),
		BuildID:      contract.BuildID,
	}
	if !IsNativeSurface(binding.Surface) {
		return EnrollmentBuildBinding{}, ProvenanceSurfaceMismatch
	}
	candidate := catalog.Clone()
	candidate.EnrollmentBindings = append(candidate.EnrollmentBindings, binding)
	finding := VerifyBuildProvenance(contract, enrollment, candidate)
	if finding != ProvenanceVerified {
		return EnrollmentBuildBinding{}, finding
	}
	return binding, finding
}
