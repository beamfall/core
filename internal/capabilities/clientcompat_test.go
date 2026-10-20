package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// testBuildSequence is the release ordinal every catalog record in these tests
// carries, and testMinimumSequence the matching per-surface rollback floor.
const (
	testBuildSequence    = 2
	testMinimumSequence  = 2
	testSupersededSeq    = 1
	testEnrollmentSubjID = "session:test-enrollment"
)

func testBuildID(surface Surface) string {
	return string(surface) + "-build-2"
}

func testArtifactHash(buildID string) string {
	sum := sha256.Sum256([]byte(buildID))
	return hex.EncodeToString(sum[:])
}

func testCatalog() BuildCatalog {
	catalog := BuildCatalog{MinimumSequence: map[Surface]int{}}
	for surface := range knownSurfaces {
		buildID := testBuildID(surface)
		catalog.Builds = append(catalog.Builds, BuildRecord{
			BuildID:       buildID,
			Surface:       surface,
			ArtifactHash:  testArtifactHash(buildID),
			Sequence:      testBuildSequence,
			Distributions: []Distribution{DistributionDirect, DistributionAppStore},
			Roles:         []EnrollmentRole{EnrollmentRoleViewer, EnrollmentRoleManager},
		})
		catalog.EnrollmentBindings = append(catalog.EnrollmentBindings, EnrollmentBuildBinding{
			EnrollmentID: testEnrollmentSubjID,
			Surface:      surface,
			BuildID:      buildID,
		})
		catalog.MinimumSequence[surface] = testMinimumSequence
	}
	return catalog
}

func testEnrollment(distribution Distribution) Enrollment {
	return Enrollment{
		ID:           testEnrollmentSubjID,
		Role:         EnrollmentRoleViewer,
		Distribution: distribution,
	}
}

// retarget moves a contract to another surface together with the build
// provenance that surface is entitled to.
func retarget(contract ClientContract, surface Surface) ClientContract {
	contract.Surface = string(surface)
	contract.BuildID = testBuildID(surface)
	contract.ArtifactHash = testArtifactHash(contract.BuildID)
	return contract
}

func testPolicy() Policy {
	return Policy{
		Builds:                      testCatalog(),
		SupportedCoreRevisions:      [2]Revision{2, 3},
		SupportedRenderingRevisions: [2]Revision{4, 5},
		RequiredRenderingStamps:     []string{"renderer-v4"},
		RequiredVocabularies:        []string{"media-v2"},
		KnownCapabilities:           map[string]bool{"search": true},
		SupportedDeveloperAPIMajor:  1,
		RevokedBuilds:               map[string]bool{"revoked": true},
		EmbeddedUIRevision:          2,
		Generation:                  7,
	}
}

func testContract(surface Surface) ClientContract {
	return ClientContract{
		Surface:                   string(surface),
		AppVersion:                "1.2.3",
		BuildID:                   testBuildID(surface),
		ArtifactHash:              testArtifactHash(testBuildID(surface)),
		CoreContractRevRange:      [2]Revision{2, 3},
		RenderingContractRevRange: [2]Revision{4, 5},
		RenderingStamps:           []string{"renderer-v4"},
		FixedVocabularies:         []string{"media-v2"},
		RequestedCapabilities:     []string{"search"},
		DeveloperAPIMajor:         1,
	}
}

func TestEvaluateYieldsEveryDecision(t *testing.T) {
	policy := testPolicy()
	tests := []struct {
		name     string
		contract ClientContract
		want     Decision
		reason   ReasonCode
	}{
		{
			name:     "compatible",
			contract: testContract(SurfaceIOSViewer),
			want:     DecisionCompatible,
		},
		{
			name: "degraded",
			contract: func() ClientContract {
				contract := testContract(SurfaceIOSViewer)
				contract.RequestedCapabilities = append(contract.RequestedCapabilities, "future")
				return contract
			}(),
			want:   DecisionDegraded,
			reason: ReasonUnsupportedCapability,
		},
		{
			name: "client too old",
			contract: func() ClientContract {
				contract := testContract(SurfaceIOSViewer)
				contract.CoreContractRevRange = [2]Revision{1, 1}
				return contract
			}(),
			want:   DecisionClientTooOld,
			reason: ReasonCoreTooOld,
		},
		{
			name: "server too old",
			contract: func() ClientContract {
				contract := testContract(SurfaceIOSViewer)
				contract.CoreContractRevRange = [2]Revision{6, 7}
				return contract
			}(),
			want:   DecisionServerTooOld,
			reason: ReasonCoreTooNew,
		},
		{
			name: "server installation inconsistent",
			contract: func() ClientContract {
				contract := testContract(SurfaceCoreWebUI)
				contract.CoreContractRevRange = [2]Revision{3, 3}
				return contract
			}(),
			want:   DecisionServerInstallationInconsistent,
			reason: ReasonEmbeddedUISkew,
		},
		{
			name: "unknown surface fails closed",
			contract: func() ClientContract {
				contract := testContract(SurfaceIOSViewer)
				contract.Surface = "plugin-invented-surface"
				return contract
			}(),
			want:   DecisionClientTooOld,
			reason: ReasonUnknownSurface,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Evaluate(test.contract, testEnrollment(DistributionDirect), policy)
			if got.Decision != test.want || got.ReasonCode != test.reason {
				t.Fatalf("Evaluate() = (%q, %q), want (%q, %q)", got.Decision, got.ReasonCode, test.want, test.reason)
			}
			if got.Servable() != (test.want == DecisionCompatible || test.want == DecisionDegraded) {
				t.Fatalf("Result.Servable() = %t for decision %q", got.Servable(), got.Decision)
			}
		})
	}
}

func TestEmbeddedUISkewIsSurfaceSpecific(t *testing.T) {
	policy := testPolicy()
	web := testContract(SurfaceCoreWebUI)
	web.CoreContractRevRange = [2]Revision{3, 3}
	if got := Evaluate(web, testEnrollment(DistributionDirect), policy); got.Decision != DecisionServerInstallationInconsistent {
		t.Fatalf("web mismatch decision = %q, want %q", got.Decision, DecisionServerInstallationInconsistent)
	}

	ios := retarget(web, SurfaceIOSViewer)
	got := Evaluate(ios, testEnrollment(DistributionAppStore), policy)
	if got.Decision != DecisionCompatible {
		t.Fatalf("iOS overlapping range decision = %q, want %q", got.Decision, DecisionCompatible)
	}

	ios.CoreContractRevRange = [2]Revision{6, 7}
	got = Evaluate(ios, testEnrollment(DistributionAppStore), policy)
	if got.Decision != DecisionServerTooOld || got.Decision == DecisionServerInstallationInconsistent {
		t.Fatalf("iOS skew decision = %q, want ordinary %q", got.Decision, DecisionServerTooOld)
	}
}

func TestCompleteContractAndFailClosedInputs(t *testing.T) {
	policy := testPolicy()
	complete := testContract(SurfaceIOSViewer)
	if got := Evaluate(complete, testEnrollment(DistributionDirect), policy); got.Decision != DecisionCompatible {
		t.Fatalf("complete contract = (%q, %q), want compatible", got.Decision, got.ReasonCode)
	}

	tests := []struct {
		name         string
		distribution Distribution
		mutate       func(*ClientContract)
		reason       ReasonCode
	}{
		{
			name:         "unverified distribution",
			distribution: "",
			mutate:       func(*ClientContract) {},
			reason:       ReasonUnverifiedDistribution,
		},
		{
			name:         "unrecognized distribution",
			distribution: "browser-claimed",
			mutate:       func(*ClientContract) {},
			reason:       ReasonUnverifiedDistribution,
		},
		{
			name:         "inverted core revision range",
			distribution: DistributionDirect,
			mutate: func(contract *ClientContract) {
				contract.CoreContractRevRange = [2]Revision{3, 2}
			},
			reason: ReasonUnknownBuild,
		},
		{
			name:         "invalid rendering revision range",
			distribution: DistributionDirect,
			mutate: func(contract *ClientContract) {
				contract.RenderingContractRevRange = [2]Revision{2, 1}
			},
			reason: ReasonUnknownBuild,
		},
		{
			name:         "missing rendering stamp",
			distribution: DistributionDirect,
			mutate:       func(contract *ClientContract) { contract.RenderingStamps = nil },
			reason:       ReasonMissingRenderingStamp,
		},
		{
			name:         "missing vocabulary",
			distribution: DistributionDirect,
			mutate:       func(contract *ClientContract) { contract.FixedVocabularies = nil },
			reason:       ReasonMissingVocabulary,
		},
		{
			name:         "revoked build",
			distribution: DistributionDirect,
			mutate:       func(contract *ClientContract) { contract.BuildID = "revoked" },
			reason:       ReasonRevokedBuild,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := complete
			test.mutate(&contract)
			got := Evaluate(contract, testEnrollment(test.distribution), policy)
			if got.Servable() || got.ReasonCode != test.reason {
				t.Fatalf("Evaluate() = (%q, %q), want refusal reason %q", got.Decision, got.ReasonCode, test.reason)
			}
		})
	}
}

func TestDegradedDoesNotReflectUnknownRequestedCapabilities(t *testing.T) {
	contract := testContract(SurfaceIOSViewer)
	contract.RequestedCapabilities = []string{"future-b", "search", "future-a", "future-a"}
	got := Evaluate(contract, testEnrollment(DistributionDirect), testPolicy())
	if got.Decision != DecisionDegraded || got.ReasonCode != ReasonUnsupportedCapability {
		t.Fatalf("Evaluate() = %#v, want degraded unknown-capability", got)
	}
	if len(got.DisabledCapabilities) != 0 {
		t.Fatalf("disabled capabilities reflected caller input: %v", got.DisabledCapabilities)
	}
}

func TestContractHashCanonicalAndDistributionBound(t *testing.T) {
	first := testContract(SurfaceIOSViewer)
	first.RenderingStamps = []string{"b", "a", "a"}
	second := first
	second.RenderingStamps = []string{"a", "b"}
	if ContractHash(first, DistributionDirect) != ContractHash(second, DistributionDirect) {
		t.Fatal("set ordering changed the canonical contract hash")
	}
	if ContractHash(first, DistributionDirect) == ContractHash(first, DistributionAppStore) {
		t.Fatal("distribution did not change the contract hash")
	}
}

func TestRegistryReplaceRefuseAndResolveBindings(t *testing.T) {
	registry := NewRegistry()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	key := BindingKey{
		Subject:         "session-1",
		Surface:         string(SurfaceIOSViewer),
		Distribution:    DistributionAppStore,
		AuthFingerprint: "auth-v1",
		Generation:      7,
	}
	contract := testContract(SurfaceIOSViewer)
	accepted, ok, err := registry.Accept(key, contract, Result{Decision: DecisionCompatible}, now, time.Minute)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !ok || !accepted.Submitted || accepted.DecisionID == "" {
		t.Fatalf("Accept() = (%+v, %t), want submitted accepted record", accepted, ok)
	}

	want := ResolveKey{
		Subject:         key.Subject,
		Distribution:    key.Distribution,
		AuthFingerprint: key.AuthFingerprint,
		Generation:      key.Generation,
	}
	resolved, err := registry.Resolve(key.Subject, want, now)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Contract.Surface != string(SurfaceIOSViewer) {
		t.Fatalf("stored surface = %q, want pinned iOS surface", resolved.Contract.Surface)
	}

	tests := []struct {
		name string
		want ResolveKey
		at   time.Time
		err  error
	}{
		{
			name: "auth changed",
			want: ResolveKey{Subject: key.Subject, Distribution: key.Distribution, AuthFingerprint: "auth-v2", Generation: key.Generation},
			at:   now,
			err:  ErrAuthChanged,
		},
		{
			name: "generation changed",
			want: ResolveKey{Subject: key.Subject, Distribution: key.Distribution, AuthFingerprint: key.AuthFingerprint, Generation: key.Generation + 1},
			at:   now,
			err:  ErrStaleGeneration,
		},
		{
			name: "expired",
			want: want,
			at:   now.Add(time.Minute),
			err:  ErrExpired,
		},
		{
			name: "distribution changed",
			want: ResolveKey{
				Subject:         key.Subject,
				Distribution:    DistributionDirect,
				AuthFingerprint: key.AuthFingerprint,
				Generation:      key.Generation,
			},
			at:  now,
			err: ErrDistributionChanged,
		},
		{
			name: "derived surface changed",
			want: ResolveKey{
				Subject:         key.Subject,
				Distribution:    key.Distribution,
				AuthFingerprint: key.AuthFingerprint,
				Generation:      key.Generation,
				DerivedSurface:  string(SurfaceCoreWebUI),
			},
			at:  now,
			err: ErrWrongSurface,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, err := registry.Resolve(key.Subject, test.want, test.at)
			if !errors.Is(err, test.err) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.err)
			}
			if record.Key.Subject != key.Subject {
				t.Fatalf("Resolve() mismatch record subject = %q, want %q", record.Key.Subject, key.Subject)
			}
		})
	}

	refused, ok, err := registry.Accept(
		key,
		contract,
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild},
		now.Add(time.Second),
		time.Minute,
	)
	if err != nil {
		t.Fatalf("refused Accept() error = %v", err)
	}
	if ok || refused.Result.Decision != DecisionClientTooOld {
		t.Fatalf("refused Accept() = (%+v, %t)", refused, ok)
	}
	// A refusal used to leave the subject indistinguishable from one that never
	// handshook, which is what let a restart revive its already-issued tokens
	//. It now resolves as
	// ErrInvalidated, and must NOT resolve as ErrNoDecision.
	after, err := registry.Resolve(key.Subject, want, now.Add(2*time.Second))
	if !errors.Is(err, ErrInvalidated) {
		t.Fatalf("Resolve() after refusal error = %v, want %v", err, ErrInvalidated)
	}
	if errors.Is(err, ErrNoDecision) {
		t.Fatalf("Resolve() after refusal reported the subject as unknown: %v", err)
	}
	if after.Result.Decision != DecisionClientTooOld || after.Result.ReasonCode != ReasonRevokedBuild {
		t.Fatalf("Resolve() after refusal result = %+v, want the original refusal", after.Result)
	}
	// The record itself is still gone: the subject is refused, not served.
	if after.Submitted || after.DecisionID != "" {
		t.Fatalf("Resolve() after refusal returned a servable record: %+v", after)
	}
}

func TestRegistryRejectsUnboundAndStaleAcceptsWithoutReplacingFreshRecord(t *testing.T) {
	registry := NewRegistry()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	key := BindingKey{
		Subject:         "session-1",
		Surface:         string(SurfaceIOSViewer),
		Distribution:    DistributionAppStore,
		AuthFingerprint: "auth-v1",
		Generation:      8,
	}
	contract := testContract(SurfaceIOSViewer)
	if err := registry.Invalidate(key.Generation); err != nil {
		t.Fatalf("Invalidate() = %v", err)
	}
	fresh, accepted, err := registry.Accept(
		key,
		contract,
		Result{Decision: DecisionCompatible},
		now,
		time.Minute,
	)
	if err != nil || !accepted {
		t.Fatalf("fresh Accept() = (%+v, %t, %v)", fresh, accepted, err)
	}

	mismatched := contract
	mismatched.Surface = string(SurfaceAndroidViewer)
	if _, _, err := registry.Accept(key, mismatched, Result{Decision: DecisionCompatible}, now, time.Minute); !errors.Is(err, ErrWrongSurface) {
		t.Fatalf("surface-mismatched Accept() error = %v, want %v", err, ErrWrongSurface)
	}

	staleKey := key
	staleKey.Generation--
	if _, _, err := registry.Accept(
		staleKey,
		contract,
		Result{Decision: DecisionClientTooOld, ReasonCode: ReasonRevokedBuild},
		now,
		time.Minute,
	); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale Accept() error = %v, want %v", err, ErrStaleGeneration)
	}

	resolved, err := registry.Resolve(key.Subject, ResolveKey{
		Subject:         key.Subject,
		Distribution:    key.Distribution,
		AuthFingerprint: key.AuthFingerprint,
		Generation:      key.Generation,
	}, now)
	if err != nil {
		t.Fatalf("Resolve() fresh record after rejected accepts: %v", err)
	}
	if resolved.DecisionID != fresh.DecisionID {
		t.Fatal("rejected accept replaced the fresh compatibility record")
	}
}

func TestRegistryDerivedRecordAndInvalidation(t *testing.T) {
	var registry Registry
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	key := BindingKey{
		Subject:         "cookie-1",
		Surface:         string(SurfaceCoreWebUI),
		Distribution:    DistributionDirect,
		AuthFingerprint: "auth",
		Generation:      3,
	}
	record, ok, err := registry.AcceptDerived(
		key,
		EmbeddedUIContract(),
		Result{Decision: DecisionCompatible},
		now,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("AcceptDerived() error = %v", err)
	}
	if !ok || record.Submitted {
		t.Fatalf("AcceptDerived() = (%+v, %t), want non-submitted accepted record", record, ok)
	}
	if err := registry.Invalidate(4); err != nil {
		t.Fatalf("Invalidate() = %v", err)
	}
	oldWant := ResolveKey{
		Subject:         key.Subject,
		Distribution:    key.Distribution,
		AuthFingerprint: key.AuthFingerprint,
		Generation:      key.Generation,
		DerivedSurface:  string(SurfaceCoreWebUI),
	}
	if _, err := registry.Resolve(key.Subject, oldWant, now); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Resolve() with old caller generation after invalidation error = %v, want %v", err, ErrStaleGeneration)
	}
	want := ResolveKey{
		Subject:         key.Subject,
		Distribution:    key.Distribution,
		AuthFingerprint: key.AuthFingerprint,
		Generation:      4,
		DerivedSurface:  string(SurfaceCoreWebUI),
	}
	record, err = registry.Resolve(key.Subject, want, now)
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("Resolve() after invalidation error = %v, want %v", err, ErrStaleGeneration)
	}
	if record.Submitted {
		t.Fatal("stale derived record lost its submission provenance")
	}
	if _, err := registry.Resolve(key.Subject, want, now.Add(31*time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired provisional after invalidation error = %v, want %v", err, ErrExpired)
	}
}

func TestRegistryReturnsCopies(t *testing.T) {
	registry := NewRegistry()
	now := time.Now()
	key := BindingKey{
		Subject:         "subject",
		Surface:         string(SurfaceIOSViewer),
		Distribution:    DistributionAppStore,
		AuthFingerprint: "auth",
		Generation:      1,
	}
	contract := testContract(SurfaceIOSViewer)
	record, ok, err := registry.Accept(key, contract, Result{
		Decision:             DecisionDegraded,
		DisabledCapabilities: []string{"future"},
	}, now, time.Minute)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !ok {
		t.Fatal("Accept() refused degraded record")
	}
	record.Contract.RenderingStamps[0] = "mutated"
	record.Result.DisabledCapabilities[0] = "mutated"

	resolved, err := registry.Resolve(key.Subject, ResolveKey{
		Subject:         key.Subject,
		Distribution:    key.Distribution,
		AuthFingerprint: key.AuthFingerprint,
		Generation:      key.Generation,
	}, now)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Contract.RenderingStamps[0] != "renderer-v4" {
		t.Fatal("caller mutated the registry's stored contract")
	}
	if resolved.Result.DisabledCapabilities[0] != "future" {
		t.Fatal("caller mutated the registry's stored result")
	}
}
