package capabilities

import (
	"strings"
	"testing"
)

// legitimateEnrolledCase is the reference case every refusal below mutates: an
// enrolled viewer on a direct build that the catalog knows.
func legitimateEnrolledCase() (ClientContract, Enrollment, Policy) {
	return testContract(SurfaceIOSViewer), testEnrollment(DistributionDirect), testPolicy()
}

func TestLegitimateEnrolledBuildIsVerifiedAndCompatible(t *testing.T) {
	contract, enrollment, policy := legitimateEnrolledCase()
	if finding := VerifyBuildProvenance(contract, enrollment, policy.Builds); finding != ProvenanceVerified {
		t.Fatalf("VerifyBuildProvenance = %q, want %q", finding, ProvenanceVerified)
	}
	result := Evaluate(contract, enrollment, policy)
	if result.Decision != DecisionCompatible {
		t.Fatalf("Evaluate = %#v, want %q", result, DecisionCompatible)
	}
	if !result.Servable() {
		t.Fatal("a legitimate enrolled build must reach a servable decision")
	}
}

// TestProvenanceRefusesUnknownMismatchedAndRolledBackBuilds asserts the refusal
// direction directly: each case must NOT be verified, must NOT be compatible,
// and must NOT be servable.
func TestProvenanceRefusesUnknownMismatchedAndRolledBackBuilds(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ClientContract, *Enrollment, *Policy)
		finding ProvenanceFinding
	}{
		{
			name: "unknown build id",
			mutate: func(contract *ClientContract, _ *Enrollment, _ *Policy) {
				contract.BuildID = "never-released-build"
			},
			finding: ProvenanceUnknownBuild,
		},
		{
			name: "build id absent from an empty catalog",
			mutate: func(_ *ClientContract, _ *Enrollment, policy *Policy) {
				policy.Builds = BuildCatalog{}
			},
			finding: ProvenanceUnknownBuild,
		},
		{
			name: "mismatched artifact hash",
			mutate: func(contract *ClientContract, _ *Enrollment, _ *Policy) {
				contract.ArtifactHash = testArtifactHash("some-other-artifact")
			},
			finding: ProvenanceArtifactMismatch,
		},
		{
			name: "absent artifact hash",
			mutate: func(contract *ClientContract, _ *Enrollment, _ *Policy) {
				contract.ArtifactHash = ""
			},
			finding: ProvenanceArtifactMismatch,
		},
		{
			name: "absent artifact hash on both sides",
			mutate: func(contract *ClientContract, _ *Enrollment, policy *Policy) {
				contract.ArtifactHash = ""
				policy.Builds = catalogWith(policy.Builds, contract.BuildID, func(record *BuildRecord) {
					record.ArtifactHash = ""
				})
			},
			finding: ProvenanceUnusableRecord,
		},
		{
			name: "truncated artifact hash",
			mutate: func(contract *ClientContract, _ *Enrollment, _ *Policy) {
				contract.ArtifactHash = contract.ArtifactHash[:artifactHashHexLength-1]
			},
			finding: ProvenanceArtifactMismatch,
		},
		{
			name: "rolled back build below the surface floor",
			mutate: func(contract *ClientContract, _ *Enrollment, policy *Policy) {
				policy.Builds = catalogWith(policy.Builds, contract.BuildID, func(record *BuildRecord) {
					record.Sequence = testSupersededSeq
				})
			},
			finding: ProvenanceSuperseded,
		},
		{
			name: "rolled back build after the floor advances",
			mutate: func(_ *ClientContract, _ *Enrollment, policy *Policy) {
				policy.Builds.MinimumSequence[SurfaceIOSViewer] = testBuildSequence + 1
			},
			finding: ProvenanceSuperseded,
		},
		{
			name: "build claimed for another surface",
			mutate: func(contract *ClientContract, _ *Enrollment, _ *Policy) {
				contract.BuildID = testBuildID(SurfaceTVOSViewer)
				contract.ArtifactHash = testArtifactHash(contract.BuildID)
			},
			finding: ProvenanceSurfaceMismatch,
		},
		{
			name: "build differs from persisted device surface",
			mutate: func(_ *ClientContract, enrollment *Enrollment, _ *Policy) {
				enrollment.Surface = SurfaceTVOSViewer
			},
			finding: ProvenanceDeviceSurfaceMismatch,
		},
		{
			name: "ambiguous duplicate catalog records",
			mutate: func(contract *ClientContract, _ *Enrollment, policy *Policy) {
				duplicate := findRecord(policy.Builds, contract.BuildID)
				duplicate.Sequence = testSupersededSeq
				policy.Builds.Builds = append(policy.Builds.Builds, duplicate)
			},
			finding: ProvenanceUnusableRecord,
		},
		{
			name: "record with a zero release ordinal",
			mutate: func(contract *ClientContract, _ *Enrollment, policy *Policy) {
				policy.Builds = catalogWith(policy.Builds, contract.BuildID, func(record *BuildRecord) {
					record.Sequence = 0
				})
			},
			finding: ProvenanceUnusableRecord,
		},
		{
			name: "record entitling no enrollment role",
			mutate: func(contract *ClientContract, _ *Enrollment, policy *Policy) {
				policy.Builds = catalogWith(policy.Builds, contract.BuildID, func(record *BuildRecord) {
					record.Roles = nil
				})
			},
			finding: ProvenanceUnusableRecord,
		},
		{
			name: "enrollment role the build does not entitle",
			mutate: func(_ *ClientContract, enrollment *Enrollment, _ *Policy) {
				enrollment.Role = EnrollmentRoleAPIKey
			},
			finding: ProvenanceNotEntitled,
		},
		{
			name: "distribution class the build does not entitle",
			mutate: func(contract *ClientContract, enrollment *Enrollment, policy *Policy) {
				enrollment.Distribution = DistributionAppStore
				policy.Builds = catalogWith(policy.Builds, contract.BuildID, func(record *BuildRecord) {
					record.Distributions = []Distribution{DistributionDirect}
				})
			},
			finding: ProvenanceNotEntitled,
		},
		{
			name: "same enrollment classes without an exact build mapping",
			mutate: func(_ *ClientContract, enrollment *Enrollment, _ *Policy) {
				enrollment.ID = "session:another-enrollment"
			},
			finding: ProvenanceEnrollmentMismatch,
		},
		{
			name: "enrollment mapped to another build",
			mutate: func(_ *ClientContract, _ *Enrollment, policy *Policy) {
				for i := range policy.Builds.EnrollmentBindings {
					binding := &policy.Builds.EnrollmentBindings[i]
					if binding.EnrollmentID == testEnrollmentSubjID && binding.Surface == SurfaceIOSViewer {
						binding.BuildID = testBuildID(SurfaceTVOSViewer)
					}
				}
			},
			finding: ProvenanceEnrollmentMismatch,
		},
		{
			name: "enrollment without an identity",
			mutate: func(_ *ClientContract, enrollment *Enrollment, _ *Policy) {
				enrollment.ID = ""
			},
			finding: ProvenanceUnverifiedEnrollment,
		},
		{
			name: "enrollment outside the role vocabulary",
			mutate: func(_ *ClientContract, enrollment *Enrollment, _ *Policy) {
				enrollment.Role = "self-declared"
			},
			finding: ProvenanceUnverifiedEnrollment,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract, enrollment, policy := legitimateEnrolledCase()
			test.mutate(&contract, &enrollment, &policy)

			finding := VerifyBuildProvenance(contract, enrollment, policy.Builds)
			if finding == ProvenanceVerified {
				t.Fatal("provenance verified a build it must refuse")
			}
			if finding != test.finding {
				t.Fatalf("VerifyBuildProvenance = %q, want %q", finding, test.finding)
			}

			result := Evaluate(contract, enrollment, policy)
			if result.Decision == DecisionCompatible {
				t.Fatalf("Evaluate reached %q for a build without provenance", DecisionCompatible)
			}
			if result.Servable() {
				t.Fatalf("Evaluate = %#v, want a refusal that is not servable", result)
			}
		})
	}
}

// TestArtifactHashComparisonIsCaseAndPaddingStable proves the normalization
// path cannot be used to dodge the digest comparison in either direction.
func TestArtifactHashComparisonIsCaseAndPaddingStable(t *testing.T) {
	contract, enrollment, policy := legitimateEnrolledCase()
	contract.ArtifactHash = "  " + strings.ToUpper(contract.ArtifactHash) + "  "
	if result := Evaluate(contract, enrollment, policy); result.Decision != DecisionCompatible {
		t.Fatalf("Evaluate = %#v, want the normalized digest to match", result)
	}

	padded := contract
	padded.ArtifactHash = testArtifactHash("other") + " "
	if result := Evaluate(padded, enrollment, policy); result.Servable() {
		t.Fatalf("Evaluate = %#v, want a refusal for a non-matching digest", result)
	}
}

// TestEmbeddedUIBuildIsSelfConsistent proves the compiled-in Core Web UI
// contract verifies against the compiled-in catalog record and against nothing
// else.
func TestEmbeddedUIBuildIsSelfConsistent(t *testing.T) {
	catalog := BuildCatalog{}.WithEmbeddedUIBuild()
	enrollment := Enrollment{ID: "session:web", Role: EnrollmentRoleViewer, Distribution: DistributionDirect, EmbeddedUI: true}
	if finding := VerifyBuildProvenance(EmbeddedUIContract(), enrollment, catalog); finding != ProvenanceVerified {
		t.Fatalf("embedded UI provenance = %q, want %q", finding, ProvenanceVerified)
	}
	native := enrollment
	native.EmbeddedUI = false
	if finding := VerifyBuildProvenance(EmbeddedUIContract(), native, catalog); finding != ProvenanceEnrollmentMismatch {
		t.Fatalf("native credential claiming embedded UI provenance = %q, want %q", finding, ProvenanceEnrollmentMismatch)
	}

	impostor := EmbeddedUIContract()
	impostor.ArtifactHash = testArtifactHash("impostor-bundle")
	if finding := VerifyBuildProvenance(impostor, enrollment, catalog); finding != ProvenanceArtifactMismatch {
		t.Fatalf("impostor embedded UI provenance = %q, want %q", finding, ProvenanceArtifactMismatch)
	}
}

// TestEmbeddedUIBuildSurvivesCatalogInstallation proves an installed catalog
// cannot drop or redefine the build this binary itself ships.
func TestEmbeddedUIBuildSurvivesCatalogInstallation(t *testing.T) {
	hostile := BuildCatalog{
		Builds: []BuildRecord{{
			BuildID:       embeddedUIBuildID,
			Surface:       SurfaceCoreWebUI,
			ArtifactHash:  testArtifactHash("substituted-bundle"),
			Sequence:      9,
			Distributions: []Distribution{DistributionDirect},
			Roles:         []EnrollmentRole{EnrollmentRoleViewer},
		}},
		EnrollmentBindings: []EnrollmentBuildBinding{{
			EnrollmentID: testEnrollmentSubjID,
			Surface:      SurfaceCoreWebUI,
			BuildID:      embeddedUIBuildID,
		}},
	}
	merged := hostile.WithEmbeddedUIBuild()
	record := findRecord(merged, embeddedUIBuildID)
	if record.ArtifactHash != EmbeddedUIBuildRecord().ArtifactHash {
		t.Fatalf("installed catalog redefined the embedded build digest: %q", record.ArtifactHash)
	}
	if count := len(merged.match(embeddedUIBuildID)); count != 1 {
		t.Fatalf("embedded build record count = %d, want 1", count)
	}
	if len(merged.EnrollmentBindings) != 0 {
		t.Fatalf("operator catalog retained embedded UI enrollment bindings: %#v", merged.EnrollmentBindings)
	}
}

// TestCatalogDigestTracksEveryInput proves a catalog change advances the digest
// that the compatibility generation is derived from.
func TestCatalogDigestTracksEveryInput(t *testing.T) {
	base := testCatalog()
	baseline := base.Digest()

	mutations := map[string]func(*BuildCatalog){
		"added record": func(catalog *BuildCatalog) {
			catalog.Builds = append(catalog.Builds, BuildRecord{
				BuildID:       "extra-build",
				Surface:       SurfaceIOSViewer,
				ArtifactHash:  testArtifactHash("extra-build"),
				Sequence:      3,
				Distributions: []Distribution{DistributionDirect},
				Roles:         []EnrollmentRole{EnrollmentRoleViewer},
			})
		},
		"changed digest": func(catalog *BuildCatalog) {
			catalog.Builds[0].ArtifactHash = testArtifactHash("rebuilt")
		},
		"changed release ordinal": func(catalog *BuildCatalog) {
			catalog.Builds[0].Sequence = testBuildSequence + 5
		},
		"changed rollback floor": func(catalog *BuildCatalog) {
			catalog.MinimumSequence[SurfaceIOSViewer] = testBuildSequence + 5
		},
		"changed entitlement": func(catalog *BuildCatalog) {
			catalog.Builds[0].Roles = []EnrollmentRole{EnrollmentRoleAdminToken}
		},
		"changed enrollment binding": func(catalog *BuildCatalog) {
			catalog.EnrollmentBindings[0].EnrollmentID = "session:another-enrollment"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := base.Clone()
			mutate(&mutated)
			if mutated.Digest() == baseline {
				t.Fatal("catalog digest did not change with the catalog")
			}
		})
	}

	if base.Clone().Digest() != baseline {
		t.Fatal("catalog digest is not stable across an unchanged clone")
	}
}

func catalogWith(catalog BuildCatalog, buildID string, mutate func(*BuildRecord)) BuildCatalog {
	updated := catalog.Clone()
	for i := range updated.Builds {
		if updated.Builds[i].BuildID != buildID {
			continue
		}
		mutate(&updated.Builds[i])
	}
	return updated
}

func findRecord(catalog BuildCatalog, buildID string) BuildRecord {
	for _, record := range catalog.Builds {
		if record.BuildID == buildID {
			return record
		}
	}
	return BuildRecord{}
}
