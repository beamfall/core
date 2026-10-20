package conformance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/capabilities"
)

const clientCompatGoldenPath = "testdata/client-compat-decisions.golden.json"

var (
	clientCompatSurfaces = []string{
		"core-web-ui",
		"macos-manager",
		"ios-viewer",
		"ipad-manager",
		"tvos-viewer",
		"android-viewer",
		"developer-api-client",
		"origin-node",
	}
	clientCompatDecisions = []string{
		"compatible",
		"degraded",
		"client-too-old",
		"server-too-old",
		"server-installation-inconsistent",
	}
	clientCompatReasons = []string{
		"unknown-surface",
		"unknown-build",
		"revoked-build",
		"unverified-distribution",
		"embedded-ui-skew",
		"core-revision-too-old",
		"core-revision-too-new",
		"rendering-revision-too-old",
		"rendering-revision-too-new",
		"missing-rendering-stamp",
		"missing-vocabulary",
		"unsupported-api-major",
		"unknown-capability",
	}
	clientCompatMessageClasses = []string{
		"compat-bootstrap",
		"compat-compatible",
		"compat-degraded",
		"compat-client-too-old",
		"compat-server-too-old",
		"compat-server-installation-inconsistent",
	}
)

type clientCompatArtifact struct {
	SchemaVersion              int      `json:"schemaVersion"`
	ContractVersion            string   `json:"contractVersion"`
	SurfaceVocabulary          []string `json:"surfaceVocabulary"`
	DistributionVocabulary     []string `json:"distributionVocabulary"`
	CoreContractRevision       revision `json:"coreContractRevision"`
	RenderingContractRevision  revision `json:"renderingContractRevision"`
	EmbeddedUIContractRevision int      `json:"embeddedUIContractRevision"`
	RequiredRenderingStamps    []string `json:"requiredRenderingStamps"`
	RequiredVocabularies       []string `json:"requiredVocabularies"`
	DecisionVocabulary         []string `json:"decisionVocabulary"`
	ResponseStateVocabulary    []string `json:"responseStateVocabulary"`
	ReasonCodeVocabulary       []string `json:"reasonCodeVocabulary"`
	MessageClassVocabulary     []string `json:"messageClassVocabulary"`
}

type revision struct {
	Minimum int `json:"minimum"`
	Maximum int `json:"maximum"`
}

type clientCompatFixture struct {
	SchemaVersion   int                       `json:"schemaVersion"`
	ContractVersion string                    `json:"contractVersion"`
	Cases           []clientCompatFixtureCase `json:"cases"`
}

type clientCompatFixtureCase struct {
	Name                 string                      `json:"name"`
	Contract             capabilities.ClientContract `json:"contract"`
	Distribution         capabilities.Distribution   `json:"distribution"`
	Decision             capabilities.Decision       `json:"decision"`
	ReasonCode           capabilities.ReasonCode     `json:"reasonCode,omitempty"`
	DisabledCapabilities []string                    `json:"disabledCapabilities,omitempty"`
	MessageClass         string                      `json:"messageClass"`
}

func TestClientCompatClosedVocabularyAndDecisions(t *testing.T) {
	policy := clientCompatPolicy()
	for _, surface := range clientCompatSurfaces {
		contract := clientCompatRetarget(clientCompatContract(), surface)
		if result := capabilities.Evaluate(contract, clientCompatEnrollment(capabilities.DistributionDirect), policy); result.Decision != capabilities.DecisionCompatible {
			t.Errorf("surface %q = %#v, want compatible", surface, result)
		}
	}

	unknown := clientCompatContract()
	unknown.Surface = "plugin-added-surface"
	assertCompatResult(t, capabilities.Evaluate(unknown, clientCompatEnrollment(capabilities.DistributionDirect), policy), capabilities.DecisionClientTooOld, capabilities.ReasonUnknownSurface)

	cases := []struct {
		name     string
		mutate   func(*capabilities.ClientContract)
		decision capabilities.Decision
		reason   capabilities.ReasonCode
	}{
		{"compatible", func(*capabilities.ClientContract) {}, capabilities.DecisionCompatible, ""},
		{"degraded", func(c *capabilities.ClientContract) {
			c.RequestedCapabilities = append(c.RequestedCapabilities, "unknown-capability")
		}, capabilities.DecisionDegraded, capabilities.ReasonUnsupportedCapability},
		{"client-too-old", func(c *capabilities.ClientContract) {
			c.CoreContractRevRange = [2]capabilities.Revision{0, 0}
		}, capabilities.DecisionClientTooOld, capabilities.ReasonCoreTooOld},
		{"server-too-old", func(c *capabilities.ClientContract) {
			c.CoreContractRevRange = [2]capabilities.Revision{2, 2}
		}, capabilities.DecisionServerTooOld, capabilities.ReasonCoreTooNew},
		{"server-installation-inconsistent", func(c *capabilities.ClientContract) {
			*c = clientCompatRetarget(*c, string(capabilities.SurfaceCoreWebUI))
			c.CoreContractRevRange = [2]capabilities.Revision{2, 2}
		}, capabilities.DecisionServerInstallationInconsistent, capabilities.ReasonEmbeddedUISkew},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := clientCompatContract()
			tc.mutate(&contract)
			assertCompatResult(t, capabilities.Evaluate(contract, clientCompatEnrollment(capabilities.DistributionDirect), policy), tc.decision, tc.reason)
		})
	}

	native := clientCompatRetarget(clientCompatContract(), string(capabilities.SurfaceMacOSManager))
	native.CoreContractRevRange = [2]capabilities.Revision{2, 2}
	result := capabilities.Evaluate(native, clientCompatEnrollment(capabilities.DistributionDirect), policy)
	if result.Decision == capabilities.DecisionServerInstallationInconsistent {
		t.Fatal("server-installation-inconsistent escaped the core-web-ui surface")
	}
}

func TestClientCompatFailClosedInputs(t *testing.T) {
	policy := clientCompatPolicy()
	cases := []struct {
		name         string
		distribution capabilities.Distribution
		mutate       func(*capabilities.ClientContract, *capabilities.Policy)
		reason       capabilities.ReasonCode
	}{
		{"unverified-distribution", "sideloaded", func(*capabilities.ClientContract, *capabilities.Policy) {}, capabilities.ReasonUnverifiedDistribution},
		{"unknown-build", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.BuildID = ""
		}, capabilities.ReasonUnknownBuild},
		{"revoked-build", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.BuildID = "revoked-build"
		}, capabilities.ReasonRevokedBuild},
		{"missing-rendering-stamp", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.RenderingStamps = nil
		}, capabilities.ReasonMissingRenderingStamp},
		{"missing-vocabulary", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.FixedVocabularies = nil
		}, capabilities.ReasonMissingVocabulary},
		{"rendering-too-old", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.RenderingContractRevRange = [2]capabilities.Revision{0, 0}
		}, capabilities.ReasonRenderingTooOld},
		{"rendering-too-new", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.RenderingContractRevRange = [2]capabilities.Revision{2, 2}
		}, capabilities.ReasonRenderingTooNew},
		{"unsupported-api-major", capabilities.DistributionDirect, func(c *capabilities.ClientContract, _ *capabilities.Policy) {
			c.DeveloperAPIMajor = 2
		}, capabilities.ReasonUnsupportedAPIMajor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract, testPolicy := clientCompatContract(), policy
			tc.mutate(&contract, &testPolicy)
			result := capabilities.Evaluate(contract, clientCompatEnrollment(tc.distribution), testPolicy)
			if result.Servable() || result.ReasonCode != tc.reason {
				t.Fatalf("Evaluate = %#v, want refusal %q", result, tc.reason)
			}
		})
	}
}

func TestClientCompatDiagnosticsAreClosedAndGeneric(t *testing.T) {
	policy := clientCompatPolicy()
	contract := clientCompatContract()
	const unknown = "torrent-plugin-endpoint"
	contract.RequestedCapabilities = []string{"known", unknown}
	result := capabilities.Evaluate(contract, clientCompatEnrollment(capabilities.DistributionDirect), policy)
	if result.Decision != capabilities.DecisionDegraded || result.ReasonCode != capabilities.ReasonUnsupportedCapability {
		t.Fatalf("Evaluate = %#v, want degraded unknown-capability", result)
	}
	if slices.Contains(result.DisabledCapabilities, unknown) {
		t.Fatalf("unknown requested capability reflected into diagnostics: %v", result.DisabledCapabilities)
	}
	for _, disabled := range result.DisabledCapabilities {
		if !policy.KnownCapabilities[disabled] {
			t.Fatalf("disabled capability %q is outside the Core-owned vocabulary", disabled)
		}
	}
	diagnostics := append([]string(nil), clientCompatReasons...)
	diagnostics = append(diagnostics, result.DisabledCapabilities...)
	for _, diagnostic := range diagnostics {
		for _, forbidden := range []string{"plugin", "endpoint", "torrent", "acquisition"} {
			if strings.Contains(diagnostic, forbidden) {
				t.Errorf("diagnostic %q contains forbidden vocabulary %q", diagnostic, forbidden)
			}
		}
	}
}

// TestClientCompatBuildProvenanceFailsClosed is the ADR-0063 build-provenance
// gate: a compatibility decision binds to a server-known build record, not to
// the contract fields a client reports about itself. Every case asserts the
// refusal direction directly — not verified, not compatible, not servable.
func TestClientCompatBuildProvenanceFailsClosed(t *testing.T) {
	surface := string(capabilities.SurfaceMacOSManager)
	legitimate := clientCompatRetarget(clientCompatContract(), surface)
	enrollment := clientCompatEnrollment(capabilities.DistributionDirect)

	t.Run("legitimate enrolled build succeeds", func(t *testing.T) {
		policy := clientCompatPolicy()
		if finding := capabilities.VerifyBuildProvenance(legitimate, enrollment, policy.Builds); finding != capabilities.ProvenanceVerified {
			t.Fatalf("provenance = %q, want %q", finding, capabilities.ProvenanceVerified)
		}
		result := capabilities.Evaluate(legitimate, enrollment, policy)
		if result.Decision != capabilities.DecisionCompatible || !result.Servable() {
			t.Fatalf("Evaluate = %#v, want a servable compatible decision", result)
		}
	})

	cases := []struct {
		name    string
		mutate  func(*capabilities.ClientContract, *capabilities.Policy)
		finding capabilities.ProvenanceFinding
	}{
		{
			name: "unknown build id",
			mutate: func(contract *capabilities.ClientContract, _ *capabilities.Policy) {
				contract.BuildID = "build-the-server-never-released"
			},
			finding: capabilities.ProvenanceUnknownBuild,
		},
		{
			name: "mismatched artifact hash",
			mutate: func(contract *capabilities.ClientContract, _ *capabilities.Policy) {
				contract.ArtifactHash = clientCompatArtifactHash("substituted-artifact")
			},
			finding: capabilities.ProvenanceArtifactMismatch,
		},
		{
			name: "rolled back build",
			mutate: func(_ *capabilities.ClientContract, policy *capabilities.Policy) {
				for i := range policy.Builds.Builds {
					if policy.Builds.Builds[i].Surface != capabilities.Surface(surface) {
						continue
					}
					policy.Builds.Builds[i].Sequence = clientCompatSupersededSequence
				}
			},
			finding: capabilities.ProvenanceSuperseded,
		},
		{
			name: "build bound to another surface",
			mutate: func(contract *capabilities.ClientContract, _ *capabilities.Policy) {
				contract.BuildID = clientCompatBuildID(string(capabilities.SurfaceTVOSViewer))
				contract.ArtifactHash = clientCompatArtifactHash(contract.BuildID)
			},
			finding: capabilities.ProvenanceSurfaceMismatch,
		},
		{
			name: "enrollment has no exact surface-to-build mapping",
			mutate: func(_ *capabilities.ClientContract, policy *capabilities.Policy) {
				policy.Builds.EnrollmentBindings = nil
			},
			finding: capabilities.ProvenanceEnrollmentMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract, policy := legitimate, clientCompatPolicy()
			tc.mutate(&contract, &policy)

			finding := capabilities.VerifyBuildProvenance(contract, enrollment, policy.Builds)
			if finding == capabilities.ProvenanceVerified {
				t.Fatal("provenance verified a build the server must refuse")
			}
			if finding != tc.finding {
				t.Fatalf("provenance = %q, want %q", finding, tc.finding)
			}

			result := capabilities.Evaluate(contract, enrollment, policy)
			if result.Decision == capabilities.DecisionCompatible {
				t.Fatalf("Evaluate reached %q without verified provenance", capabilities.DecisionCompatible)
			}
			if result.Servable() {
				t.Fatalf("Evaluate = %#v, want a refusal that is not servable", result)
			}
			if !slices.Contains(clientCompatReasons, string(result.ReasonCode)) {
				t.Fatalf("refusal reason %q is outside the published vocabulary", result.ReasonCode)
			}
		})
	}
}

// TestClientCompatProvenanceFindingsStayLocal proves the finer-grained
// provenance vocabulary is operator-local: it never reaches the wire result,
// and it carries no acquisition-adjacent or plugin-specific wording.
func TestClientCompatProvenanceFindingsStayLocal(t *testing.T) {
	contract := clientCompatRetarget(clientCompatContract(), string(capabilities.SurfaceMacOSManager))
	contract.ArtifactHash = clientCompatArtifactHash("substituted-artifact")
	result := capabilities.Evaluate(contract, clientCompatEnrollment(capabilities.DistributionDirect), clientCompatPolicy())
	if string(result.ReasonCode) != "unknown-build" {
		t.Fatalf("wire reason = %q, want the coarse published reason", result.ReasonCode)
	}

	findings := []capabilities.ProvenanceFinding{
		capabilities.ProvenanceUnverifiedEnrollment,
		capabilities.ProvenanceUnknownBuild,
		capabilities.ProvenanceUnusableRecord,
		capabilities.ProvenanceSurfaceMismatch,
		capabilities.ProvenanceDeviceSurfaceMismatch,
		capabilities.ProvenanceArtifactMismatch,
		capabilities.ProvenanceSuperseded,
		capabilities.ProvenanceNotEntitled,
		capabilities.ProvenanceEnrollmentMismatch,
	}
	for _, finding := range findings {
		for _, forbidden := range []string{"plugin", "endpoint", "torrent", "acquisition"} {
			if strings.Contains(string(finding), forbidden) {
				t.Errorf("provenance finding %q contains forbidden vocabulary %q", finding, forbidden)
			}
		}
	}
}

func TestClientCompatBindingRejectsStaleContext(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	key := capabilities.BindingKey{
		Subject:         "session:one",
		Surface:         string(capabilities.SurfaceMacOSManager),
		Distribution:    capabilities.DistributionDirect,
		AuthFingerprint: "auth-a",
		Generation:      7,
	}
	registry := capabilities.NewRegistry()
	record, accepted, err := registry.Accept(key, clientCompatContract(), capabilities.Result{Decision: capabilities.DecisionCompatible}, now, time.Minute)
	if err != nil {
		t.Fatalf("Accept = %v", err)
	}
	if !accepted || record.DecisionID == "" {
		t.Fatalf("Accept = %#v, %v, want stored decision", record, accepted)
	}
	resolve := capabilities.ResolveKey{Subject: key.Subject, Distribution: key.Distribution, DerivedSurface: record.Key.Surface, AuthFingerprint: key.AuthFingerprint, Generation: key.Generation}
	if _, err := registry.Resolve(key.Subject, resolve, now); err != nil {
		t.Fatalf("legitimate Resolve = %v", err)
	}
	wrongSurface := resolve
	wrongSurface.DerivedSurface = "ipad-manager"
	if _, err := registry.Resolve(key.Subject, wrongSurface, now); !errors.Is(err, capabilities.ErrWrongSurface) {
		t.Fatalf("wrong-surface Resolve = %v", err)
	}
	changedAuth := resolve
	changedAuth.AuthFingerprint = "auth-b"
	if _, err := registry.Resolve(key.Subject, changedAuth, now); !errors.Is(err, capabilities.ErrAuthChanged) {
		t.Fatalf("changed-auth Resolve = %v", err)
	}
	if _, err := registry.Resolve(key.Subject, resolve, now.Add(time.Minute)); !errors.Is(err, capabilities.ErrExpired) {
		t.Fatalf("expired Resolve = %v", err)
	}
	if err := registry.Invalidate(8); err != nil {
		t.Fatalf("Invalidate() = %v", err)
	}
	if _, err := registry.Resolve(key.Subject, resolve, now); !errors.Is(err, capabilities.ErrStaleGeneration) {
		t.Fatalf("post-Invalidate Resolve = %v", err)
	}
	refusalKey := key
	refusalKey.Generation = 8
	if _, accepted, err := registry.Accept(refusalKey, clientCompatContract(), capabilities.Result{
		Decision:   capabilities.DecisionClientTooOld,
		ReasonCode: capabilities.ReasonUnknownBuild,
	}, now, time.Minute); err != nil || accepted {
		if err != nil {
			t.Fatalf("refused decision Accept = %v", err)
		}
		t.Fatal("refused decision was stored")
	}
	// The record is deleted, but the subject is NOT forgotten. Reporting a refused
	// subject as ErrNoDecision made it indistinguishable from one that never
	// handshook, which is what let a restart revive its already-issued
	// pre-binding Stream Tokens.
	refusedRecord, err := registry.Resolve(key.Subject, resolve, now)
	if !errors.Is(err, capabilities.ErrInvalidated) {
		t.Fatalf("Resolve after refusal = %v, want ErrInvalidated", err)
	}
	if errors.Is(err, capabilities.ErrNoDecision) {
		t.Fatalf("Resolve after refusal reported the subject as unknown: %v", err)
	}
	if refusedRecord.DecisionID != "" || refusedRecord.Submitted {
		t.Fatalf("Resolve after refusal returned a usable record: %+v", refusedRecord)
	}
	// A never-seen subject is still genuinely unknown: the fix must not collapse
	// the two in the other direction.
	if _, err := registry.Resolve("session-never-seen", resolve, now); !errors.Is(err, capabilities.ErrNoDecision) {
		t.Fatalf("Resolve of an unknown subject = %v, want ErrNoDecision", err)
	}
}

func TestClientCompatDecisionCarriesNoAuthorization(t *testing.T) {
	resultType := reflect.TypeOf(capabilities.Result{})
	got := make([]string, resultType.NumField())
	for i := range got {
		got[i] = resultType.Field(i).Name
	}
	want := []string{"Decision", "ReasonCode", "DisabledCapabilities"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities.Result fields = %v, want authorization-free %v", got, want)
	}
}

func TestClientCompatPublishedArtifact(t *testing.T) {
	root := repoRoot(t)
	var artifact clientCompatArtifact
	readCompatJSON(t, filepath.Join(root, "contracts/compat/v1/client-contract-revision-range.json"), &artifact)
	if artifact.SchemaVersion != 1 || artifact.ContractVersion != "1.0.0" {
		t.Fatalf("artifact version = %d/%q, want 1/1.0.0", artifact.SchemaVersion, artifact.ContractVersion)
	}
	assertStrings(t, "surface vocabulary", artifact.SurfaceVocabulary, clientCompatSurfaces)
	assertStrings(t, "distribution vocabulary", artifact.DistributionVocabulary, []string{"direct", "appStore"})
	assertStrings(t, "decision vocabulary", artifact.DecisionVocabulary, clientCompatDecisions)
	assertStrings(t, "response state vocabulary", artifact.ResponseStateVocabulary, append([]string{"bootstrap"}, clientCompatDecisions...))
	assertStrings(t, "reason vocabulary", artifact.ReasonCodeVocabulary, clientCompatReasons)
	assertStrings(t, "message classes", artifact.MessageClassVocabulary, clientCompatMessageClasses)
	if artifact.CoreContractRevision != (revision{1, 1}) ||
		artifact.RenderingContractRevision != (revision{1, 1}) ||
		artifact.EmbeddedUIContractRevision != int(capabilities.EmbeddedUIContractRevision) {
		t.Fatalf("published revision windows drifted: %#v", artifact)
	}
	embedded := capabilities.EmbeddedUIContract()
	assertStrings(t, "rendering stamps", artifact.RequiredRenderingStamps, embedded.RenderingStamps)
	assertStrings(t, "fixed vocabularies", artifact.RequiredVocabularies, embedded.FixedVocabularies)
}

func TestClientCompatGoldenDrivesProductionEvaluation(t *testing.T) {
	var fixture clientCompatFixture
	readCompatJSON(t, clientCompatGoldenPath, &fixture)
	if fixture.SchemaVersion != 1 || fixture.ContractVersion != "1.0.0" {
		t.Fatalf("fixture version = %d/%q, want 1/1.0.0", fixture.SchemaVersion, fixture.ContractVersion)
	}
	seen := map[capabilities.Decision]bool{}
	policy := clientCompatPolicy()
	policy.RequiredVocabularies = nil
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			result := capabilities.Evaluate(tc.Contract, clientCompatEnrollment(tc.Distribution), policy)
			if result.Decision != tc.Decision || result.ReasonCode != tc.ReasonCode ||
				!reflect.DeepEqual(result.DisabledCapabilities, tc.DisabledCapabilities) {
				t.Fatalf("Evaluate = %#v, golden wants decision=%q reason=%q disabled=%v", result, tc.Decision, tc.ReasonCode, tc.DisabledCapabilities)
			}
			if got := compatMessageClass(result.Decision); got != tc.MessageClass {
				t.Fatalf("message class = %q, want %q", got, tc.MessageClass)
			}
			seen[result.Decision] = true
		})
	}
	for _, decision := range clientCompatDecisions {
		if !seen[capabilities.Decision(decision)] {
			t.Errorf("golden does not produce decision %q", decision)
		}
	}
}

func TestEmbeddedUIContractRevisionMatchesWebBundle(t *testing.T) {
	sourcePath := filepath.Join(repoRoot(t), "internal/web/app/src/lib/compat.ts")
	source, err := os.ReadFile(sourcePath) //nolint:gosec // repo-relative test fixture path
	if err != nil {
		t.Fatal(err)
	}
	minimum := extractTSRevision(t, source, "CORE_CONTRACT_REV_MIN")
	maximum := extractTSRevision(t, source, "CORE_CONTRACT_REV_MAX")
	revision := int(capabilities.EmbeddedUIContractRevision)
	if minimum > revision || maximum < revision {
		t.Fatalf("web bundle core revision [%d,%d] does not bracket embedded revision %d", minimum, maximum, revision)
	}
	// The rendering revision and the renderer stamps are the second half of the
	// same cross-language contract: a bundle that brackets the core revision but
	// declares a rendering revision or stamp the Go contract does not carry is
	// refused at the handshake with embedded-ui-skew / missing-rendering-stamp.
	renderingMinimum := extractTSRevision(t, source, "RENDERING_REV_MIN")
	renderingMaximum := extractTSRevision(t, source, "RENDERING_REV_MAX")
	embedded := capabilities.EmbeddedUIContract()
	goRenderingMinimum := int(embedded.RenderingContractRevRange[0])
	goRenderingMaximum := int(embedded.RenderingContractRevRange[1])
	if renderingMinimum > goRenderingMaximum || renderingMaximum < goRenderingMinimum {
		t.Fatalf("web bundle rendering revision [%d,%d] does not overlap Go rendering revision [%d,%d]",
			renderingMinimum, renderingMaximum, goRenderingMinimum, goRenderingMaximum)
	}
	if renderingMinimum > revision || renderingMaximum < revision {
		t.Fatalf("web bundle rendering revision [%d,%d] does not bracket embedded revision %d",
			renderingMinimum, renderingMaximum, revision)
	}
	assertStrings(t, "web bundle renderer stamps", extractTSStrings(t, source, "RENDERER_STAMPS"), embedded.RenderingStamps)
}

// clientCompatBuildSequence is the release ordinal every catalog record in
// these fixtures carries; clientCompatSupersededSequence is below the matching
// per-surface rollback floor, so a record carrying it was superseded.
const (
	clientCompatBuildSequence      = 2
	clientCompatSupersededSequence = 1
)

// clientCompatEnrollment is the server-derived enrollment these fixtures
// present builds on: an enrolled viewer, never a client-supplied claim.
func clientCompatEnrollment(distribution capabilities.Distribution) capabilities.Enrollment {
	return capabilities.Enrollment{
		ID:           "session:conformance",
		Role:         capabilities.EnrollmentRoleViewer,
		Distribution: distribution,
	}
}

func clientCompatBuildID(surface string) string {
	return surface + "-build"
}

func clientCompatArtifactHash(buildID string) string {
	sum := sha256.Sum256([]byte(buildID))
	return hex.EncodeToString(sum[:])
}

// clientCompatCatalog is the server-known build record set: one legitimate
// build per surface, each pinned to its surface's rollback floor.
func clientCompatCatalog() capabilities.BuildCatalog {
	catalog := capabilities.BuildCatalog{MinimumSequence: map[capabilities.Surface]int{}}
	for _, surface := range clientCompatSurfaces {
		buildID := clientCompatBuildID(surface)
		catalog.Builds = append(catalog.Builds, capabilities.BuildRecord{
			BuildID:      buildID,
			Surface:      capabilities.Surface(surface),
			ArtifactHash: clientCompatArtifactHash(buildID),
			Sequence:     clientCompatBuildSequence,
			Distributions: []capabilities.Distribution{
				capabilities.DistributionDirect,
				capabilities.DistributionAppStore,
			},
			Roles: []capabilities.EnrollmentRole{
				capabilities.EnrollmentRoleViewer,
				capabilities.EnrollmentRoleManager,
			},
		})
		catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
			EnrollmentID: "session:conformance",
			Surface:      capabilities.Surface(surface),
			BuildID:      buildID,
		})
		catalog.MinimumSequence[capabilities.Surface(surface)] = clientCompatBuildSequence
	}
	// The golden corpus drives an embedded-UI installation-skew case: a
	// server-known core-web-ui build whose contract revision does not match
	// the installed binary. Provenance must accept it so the skew guard, not
	// the provenance guard, produces the decision.
	skewed := "embedded-core-web-ui-v2"
	catalog.Builds = append(catalog.Builds, capabilities.BuildRecord{
		BuildID:      skewed,
		Surface:      capabilities.SurfaceCoreWebUI,
		ArtifactHash: clientCompatArtifactHash(skewed),
		Sequence:     clientCompatBuildSequence,
		Distributions: []capabilities.Distribution{
			capabilities.DistributionDirect,
			capabilities.DistributionAppStore,
		},
		Roles: []capabilities.EnrollmentRole{
			capabilities.EnrollmentRoleViewer,
			capabilities.EnrollmentRoleManager,
		},
	})
	catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, capabilities.EnrollmentBuildBinding{
		EnrollmentID: "session:conformance",
		Surface:      capabilities.SurfaceCoreWebUI,
		BuildID:      skewed,
	})
	return catalog
}

// clientCompatRetarget moves a contract to another surface together with the
// build provenance that surface is entitled to.
func clientCompatRetarget(contract capabilities.ClientContract, surface string) capabilities.ClientContract {
	contract.Surface = surface
	contract.BuildID = clientCompatBuildID(surface)
	contract.ArtifactHash = clientCompatArtifactHash(contract.BuildID)
	return contract
}

func clientCompatPolicy() capabilities.Policy {
	return capabilities.Policy{
		Builds:                      clientCompatCatalog(),
		SupportedCoreRevisions:      [2]capabilities.Revision{1, 1},
		SupportedRenderingRevisions: [2]capabilities.Revision{1, 1},
		RequiredRenderingStamps:     []string{"core-web-ui-v1"},
		RequiredVocabularies:        []string{"fixed-v1"},
		KnownCapabilities:           map[string]bool{"known": true},
		SupportedDeveloperAPIMajor:  1,
		RevokedBuilds:               map[string]bool{"revoked-build": true},
		EmbeddedUIRevision:          capabilities.EmbeddedUIContractRevision,
		Generation:                  7,
	}
}

func clientCompatContract() capabilities.ClientContract {
	return capabilities.ClientContract{
		Surface:                   string(capabilities.SurfaceMacOSManager),
		AppVersion:                "1.0.0",
		BuildID:                   clientCompatBuildID(string(capabilities.SurfaceMacOSManager)),
		ArtifactHash:              clientCompatArtifactHash(clientCompatBuildID(string(capabilities.SurfaceMacOSManager))),
		CoreContractRevRange:      [2]capabilities.Revision{1, 1},
		RenderingContractRevRange: [2]capabilities.Revision{1, 1},
		RenderingStamps:           []string{"core-web-ui-v1"},
		FixedVocabularies:         []string{"fixed-v1"},
		RequestedCapabilities:     []string{"known"},
		DeveloperAPIMajor:         1,
	}
}

func assertCompatResult(t *testing.T, got capabilities.Result, decision capabilities.Decision, reason capabilities.ReasonCode) {
	t.Helper()
	if got.Decision != decision || got.ReasonCode != reason {
		t.Fatalf("Evaluate = %#v, want decision=%q reason=%q", got, decision, reason)
	}
}

func assertStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func readCompatJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // repo-relative test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

func extractTSRevision(t *testing.T, source []byte, name string) int {
	t.Helper()
	expression := regexp.MustCompile(`(?m)export const ` + regexp.QuoteMeta(name) + ` = ([0-9]+);`)
	match := expression.FindSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("%s is missing from embedded web compatibility contract", name)
	}
	var revision int
	if _, err := fmt.Sscan(string(match[1]), &revision); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return revision
}

// extractTSStrings reads a `export const NAME = ["a", "b"] as const;` literal
// from the embedded web compatibility contract.
func extractTSStrings(t *testing.T, source []byte, name string) []string {
	t.Helper()
	expression := regexp.MustCompile(`(?m)export const ` + regexp.QuoteMeta(name) + ` = \[([^\]]*)\]`)
	match := expression.FindSubmatch(source)
	if len(match) != 2 {
		t.Fatalf("%s is missing from embedded web compatibility contract", name)
	}
	values := []string{}
	for _, field := range strings.Split(string(match[1]), ",") {
		trimmed := strings.Trim(strings.TrimSpace(field), `"`)
		if trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func compatMessageClass(decision capabilities.Decision) string {
	if decision == capabilities.DecisionServerInstallationInconsistent {
		return "compat-server-installation-inconsistent"
	}
	return "compat-" + string(decision)
}
