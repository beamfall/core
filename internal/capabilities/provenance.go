package capabilities

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// artifactHashHexLength is the encoded length of a SHA-256 artifact digest.
const artifactHashHexLength = sha256.Size * 2

// EnrollmentRole is the closed vocabulary of credential classes an enrolled
// caller can present. Every value is derived from server-held enrollment state;
// a client never supplies it and cannot widen it.
type EnrollmentRole string

const (
	EnrollmentRoleViewer     EnrollmentRole = "viewer"
	EnrollmentRoleManager    EnrollmentRole = "manager"
	EnrollmentRoleAPIKey     EnrollmentRole = "api-key"
	EnrollmentRoleAdminToken EnrollmentRole = "admin-token"
)

var knownEnrollmentRoles = map[EnrollmentRole]struct{}{
	EnrollmentRoleViewer:     {},
	EnrollmentRoleManager:    {},
	EnrollmentRoleAPIKey:     {},
	EnrollmentRoleAdminToken: {},
}

var knownDistributions = map[Distribution]struct{}{
	DistributionDirect:   {},
	DistributionAppStore: {},
}

// Enrollment is the server-derived identity of the caller a Client Contract
// arrives on. It is the lookup key for the catalog's exact surface-to-build
// mapping; the remaining fields enforce credential and distribution classes.
type Enrollment struct {
	ID           string
	Role         EnrollmentRole
	Distribution Distribution
	// Surface is the server-derived persisted device surface. Empty retains the
	// bootstrap verification path used before the first native surface binding.
	Surface Surface
	// EmbeddedUI is derived from the server's web credential class. It is the
	// only enrollment mapping that does not need an entry in BuildCatalog:
	// the server already knows that credential is running its own bundle.
	EmbeddedUI bool
}

// BuildRecord is one server-known legitimate client build. The server, not the
// client, owns every field: the surface the build belongs to, the digest of the
// artifact that build ships, the release ordinal used to refuse rollbacks, and
// the enrollment classes entitled to run it.
type BuildRecord struct {
	BuildID       string
	Surface       Surface
	ArtifactHash  string
	Sequence      int
	Distributions []Distribution
	Roles         []EnrollmentRole
}

// EnrollmentBuildBinding is a server-held permission for one enrolled subject
// to present one build on one surface. Role and distribution entitlements are
// deliberately not enough: another enrollment in the same classes must not be
// able to borrow this mapping.
type EnrollmentBuildBinding struct {
	EnrollmentID string
	Surface      Surface
	BuildID      string
}

// BuildCatalog is the server-known record of legitimate client builds and the
// exact enrolled subjects allowed to present them.
// MinimumSequence is the per-surface rollback floor: a build whose release
// ordinal falls below its surface's floor was superseded and is refused even
// though it was once legitimate.
type BuildCatalog struct {
	Builds             []BuildRecord
	EnrollmentBindings []EnrollmentBuildBinding
	MinimumSequence    map[Surface]int
}

// ProvenanceFinding is the server-local audit vocabulary for a provenance
// check. It is deliberately finer-grained than the client-facing ReasonCode
// vocabulary and never reaches the wire: ADR-0063 keeps user-facing reasons
// coarse, and every non-verified finding maps to the same closed refusal.
type ProvenanceFinding string

const (
	ProvenanceVerified              ProvenanceFinding = "verified"
	ProvenanceUnverifiedEnrollment  ProvenanceFinding = "unverified-enrollment"
	ProvenanceUnknownBuild          ProvenanceFinding = "unknown-build"
	ProvenanceUnusableRecord        ProvenanceFinding = "unusable-build-record"
	ProvenanceSurfaceMismatch       ProvenanceFinding = "build-surface-mismatch"
	ProvenanceDeviceSurfaceMismatch ProvenanceFinding = "device-surface-mismatch"
	ProvenanceArtifactMismatch      ProvenanceFinding = "artifact-hash-mismatch"
	ProvenanceSuperseded            ProvenanceFinding = "superseded-build"
	ProvenanceNotEntitled           ProvenanceFinding = "enrollment-not-entitled"
	ProvenanceEnrollmentMismatch    ProvenanceFinding = "enrollment-build-mismatch"
)

// VerifyBuildProvenance binds a Client Contract to a server-known build.
// It fails closed on every path: an empty catalog knows no build, so an
// unverifiable enrollment, an unknown build id, an unusable or ambiguous
// catalog record, a build claimed for the wrong surface, an artifact digest
// that does not equal the server-known digest, a superseded release ordinal,
// an enrollment the build is not entitled to, and an enrollment without an
// exact mapping to the build each return a non-verified finding. Only the final
// path returns ProvenanceVerified.
func VerifyBuildProvenance(contract ClientContract, enrollment Enrollment, catalog BuildCatalog) ProvenanceFinding {
	if !enrollmentVerified(enrollment) {
		return ProvenanceUnverifiedEnrollment
	}
	if enrollment.Surface != "" && enrollment.Surface != Surface(strings.TrimSpace(contract.Surface)) {
		return ProvenanceDeviceSurfaceMismatch
	}
	matches := catalog.match(strings.TrimSpace(contract.BuildID))
	if len(matches) == 0 {
		return ProvenanceUnknownBuild
	}
	if len(matches) > 1 {
		return ProvenanceUnusableRecord
	}
	record := matches[0]
	if !buildRecordUsable(record) {
		return ProvenanceUnusableRecord
	}
	if record.Surface != Surface(strings.TrimSpace(contract.Surface)) {
		return ProvenanceSurfaceMismatch
	}
	if !artifactHashMatches(contract.ArtifactHash, record.ArtifactHash) {
		return ProvenanceArtifactMismatch
	}
	if record.Sequence < catalog.MinimumSequence[record.Surface] {
		return ProvenanceSuperseded
	}
	if !buildEntitles(record, enrollment) {
		return ProvenanceNotEntitled
	}
	if !catalog.boundToEnrollment(record, enrollment) {
		return ProvenanceEnrollmentMismatch
	}
	return ProvenanceVerified
}

// match returns every record carrying buildID. More than one match is an
// ambiguous catalog, never a resolved build.
func (c BuildCatalog) match(buildID string) []BuildRecord {
	if buildID == "" {
		return nil
	}
	matched := make([]BuildRecord, 0, 1)
	for _, record := range c.Builds {
		if strings.TrimSpace(record.BuildID) == buildID {
			matched = append(matched, record)
		}
	}
	return matched
}

// Clone returns a deep copy so an installed catalog cannot be mutated through
// a shared slice or map.
func (c BuildCatalog) Clone() BuildCatalog {
	builds := make([]BuildRecord, 0, len(c.Builds))
	for _, record := range c.Builds {
		record.Distributions = append([]Distribution(nil), record.Distributions...)
		record.Roles = append([]EnrollmentRole(nil), record.Roles...)
		builds = append(builds, record)
	}
	minimum := make(map[Surface]int, len(c.MinimumSequence))
	for surface, sequence := range c.MinimumSequence {
		minimum[surface] = sequence
	}
	bindings := append([]EnrollmentBuildBinding(nil), c.EnrollmentBindings...)
	return BuildCatalog{Builds: builds, EnrollmentBindings: bindings, MinimumSequence: minimum}
}

// WithEmbeddedUIBuild returns the catalog with the compiled-in Core Web UI
// record replacing any operator-supplied record of the same build id. The
// embedded UI is server-known by construction, so it cannot be removed from the
// catalog or redefined by policy installation.
//
// The embedded record takes the surface's rollback floor as its release ordinal
// whenever the floor is higher: the embedded UI is this binary's own bundle, so
// it is by definition the current Core Web UI build and can never be the
// superseded one. A floor still refuses every other core-web-ui build below it.
func (c BuildCatalog) WithEmbeddedUIBuild() BuildCatalog {
	merged := c.Clone()
	builds := make([]BuildRecord, 0, len(merged.Builds)+1)
	for _, record := range merged.Builds {
		if strings.TrimSpace(record.BuildID) == embeddedUIBuildID {
			continue
		}
		builds = append(builds, record)
	}
	bindings := make([]EnrollmentBuildBinding, 0, len(merged.EnrollmentBindings))
	for _, binding := range merged.EnrollmentBindings {
		if strings.TrimSpace(binding.BuildID) == embeddedUIBuildID {
			continue
		}
		bindings = append(bindings, binding)
	}
	embedded := EmbeddedUIBuildRecord()
	embedded.Sequence = max(embedded.Sequence, merged.MinimumSequence[SurfaceCoreWebUI])
	merged.Builds = append(builds, embedded)
	merged.EnrollmentBindings = bindings
	return merged
}

// Digest is a stable hash of every catalog input, so a catalog change advances
// the compatibility generation and invalidates decisions taken under the old
// record set.
func (c BuildCatalog) Digest() string {
	sum := sha256.New()
	writeDigestPart(sum, "builds")
	writeDigestPart(sum, strconv.Itoa(len(c.Builds)))
	for _, record := range sortedBuildRecords(c.Builds) {
		writeDigestPart(sum, record.BuildID)
		writeDigestPart(sum, string(record.Surface))
		writeDigestPart(sum, record.ArtifactHash)
		writeDigestPart(sum, strconv.Itoa(record.Sequence))
		writeDigestPart(sum, strings.Join(sortedDistributions(record.Distributions), ","))
		writeDigestPart(sum, strings.Join(sortedRoles(record.Roles), ","))
	}
	writeDigestPart(sum, "enrollment-bindings")
	writeDigestPart(sum, strconv.Itoa(len(c.EnrollmentBindings)))
	for _, binding := range sortedEnrollmentBindings(c.EnrollmentBindings) {
		writeDigestPart(sum, binding.EnrollmentID)
		writeDigestPart(sum, string(binding.Surface))
		writeDigestPart(sum, binding.BuildID)
	}
	writeDigestPart(sum, "minimum-sequences")
	writeDigestPart(sum, strconv.Itoa(len(c.MinimumSequence)))
	for _, surface := range sortedSurfaces(c.MinimumSequence) {
		writeDigestPart(sum, string(surface))
		writeDigestPart(sum, strconv.Itoa(c.MinimumSequence[surface]))
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// EmbeddedUIBuildRecord is the catalog record for the Core Web UI compiled into
// this server binary. The embedded UI ships no separate artifact, so its digest
// is derived from the build identifier the binary carries; a shipped native
// artifact carries the digest its release pipeline recorded instead.
func EmbeddedUIBuildRecord() BuildRecord {
	return BuildRecord{
		BuildID:       embeddedUIBuildID,
		Surface:       SurfaceCoreWebUI,
		ArtifactHash:  embeddedUIArtifactHash(),
		Sequence:      embeddedUIBuildSequence,
		Distributions: []Distribution{DistributionDirect, DistributionAppStore},
		Roles:         []EnrollmentRole{EnrollmentRoleViewer, EnrollmentRoleManager},
	}
}

func embeddedUIArtifactHash() string {
	sum := sha256.Sum256([]byte(embeddedUIBuildID))
	return hex.EncodeToString(sum[:])
}

func enrollmentVerified(enrollment Enrollment) bool {
	if strings.TrimSpace(enrollment.ID) == "" {
		return false
	}
	if _, ok := knownEnrollmentRoles[enrollment.Role]; !ok {
		return false
	}
	_, ok := knownDistributions[enrollment.Distribution]
	return ok
}

func buildRecordUsable(record BuildRecord) bool {
	if _, ok := knownSurfaces[record.Surface]; !ok {
		return false
	}
	if !validArtifactHash(record.ArtifactHash) {
		return false
	}
	if record.Sequence <= 0 {
		return false
	}
	if !usableDistributions(record.Distributions) {
		return false
	}
	return usableRoles(record.Roles)
}

func usableDistributions(values []Distribution) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, ok := knownDistributions[value]; !ok {
			return false
		}
	}
	return true
}

func usableRoles(values []EnrollmentRole) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, ok := knownEnrollmentRoles[value]; !ok {
			return false
		}
	}
	return true
}

// artifactHashMatches accepts only two well-formed digests that are equal.
// An absent or malformed digest on either side never matches, so an empty
// client claim cannot pair with an empty catalog field.
func artifactHashMatches(presented, known string) bool {
	if !validArtifactHash(presented) {
		return false
	}
	if !validArtifactHash(known) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(known)) == 1
}

func validArtifactHash(value string) bool {
	if len(value) != artifactHashHexLength {
		return false
	}
	if value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func buildEntitles(record BuildRecord, enrollment Enrollment) bool {
	if !slices.Contains(record.Distributions, enrollment.Distribution) {
		return false
	}
	return slices.Contains(record.Roles, enrollment.Role)
}

func (c BuildCatalog) boundToEnrollment(record BuildRecord, enrollment Enrollment) bool {
	if enrollment.EmbeddedUI && record.BuildID == embeddedUIBuildID && record.Surface == SurfaceCoreWebUI {
		return true
	}
	for _, binding := range c.EnrollmentBindings {
		if strings.TrimSpace(binding.EnrollmentID) != strings.TrimSpace(enrollment.ID) {
			continue
		}
		if binding.Surface != record.Surface {
			continue
		}
		if strings.TrimSpace(binding.BuildID) == record.BuildID {
			return true
		}
	}
	return false
}

func sortedBuildRecords(records []BuildRecord) []BuildRecord {
	sorted := append([]BuildRecord(nil), records...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].BuildID != sorted[j].BuildID {
			return sorted[i].BuildID < sorted[j].BuildID
		}
		return sorted[i].Sequence < sorted[j].Sequence
	})
	return sorted
}

func sortedEnrollmentBindings(bindings []EnrollmentBuildBinding) []EnrollmentBuildBinding {
	sorted := append([]EnrollmentBuildBinding(nil), bindings...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].EnrollmentID != sorted[j].EnrollmentID {
			return sorted[i].EnrollmentID < sorted[j].EnrollmentID
		}
		if sorted[i].Surface != sorted[j].Surface {
			return sorted[i].Surface < sorted[j].Surface
		}
		return sorted[i].BuildID < sorted[j].BuildID
	})
	return sorted
}

func sortedDistributions(values []Distribution) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	sort.Strings(out)
	return out
}

func sortedRoles(values []EnrollmentRole) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	sort.Strings(out)
	return out
}

func sortedSurfaces(values map[Surface]int) []Surface {
	out := make([]Surface, 0, len(values))
	for surface := range values {
		out = append(out, surface)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func writeDigestPart(sum interface{ Write([]byte) (int, error) }, value string) {
	_, _ = sum.Write([]byte(strconv.Itoa(len(value))))
	_, _ = sum.Write([]byte(":"))
	_, _ = sum.Write([]byte(value))
}
