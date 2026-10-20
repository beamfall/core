package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Revision is a machine-comparable client contract revision.
type Revision int

// Surface is the closed client-surface vocabulary.
type Surface string

const (
	SurfaceCoreWebUI          Surface = "core-web-ui"
	SurfaceMacOSManager       Surface = "macos-manager"
	SurfaceIOSViewer          Surface = "ios-viewer"
	SurfaceIPadManager        Surface = "ipad-manager"
	SurfaceTVOSViewer         Surface = "tvos-viewer"
	SurfaceAndroidViewer      Surface = "android-viewer"
	SurfaceDeveloperAPIClient Surface = "developer-api-client"
	SurfaceOriginNode         Surface = "origin-node"
)

var knownSurfaces = map[Surface]struct{}{
	SurfaceCoreWebUI:          {},
	SurfaceMacOSManager:       {},
	SurfaceIOSViewer:          {},
	SurfaceIPadManager:        {},
	SurfaceTVOSViewer:         {},
	SurfaceAndroidViewer:      {},
	SurfaceDeveloperAPIClient: {},
	SurfaceOriginNode:         {},
}

var nativeSurfaces = map[Surface]struct{}{
	SurfaceMacOSManager:  {},
	SurfaceIOSViewer:     {},
	SurfaceIPadManager:   {},
	SurfaceTVOSViewer:    {},
	SurfaceAndroidViewer: {},
}

// IsNativeSurface reports whether surface is in the persisted first-party
// native rollout set.
func IsNativeSurface(surface Surface) bool {
	_, ok := nativeSurfaces[surface]
	return ok
}

// Distribution is a server-derived distribution class.
type Distribution string

const (
	DistributionDirect   Distribution = "direct"
	DistributionAppStore Distribution = "appStore"
)

// Decision is the closed compatibility-decision vocabulary.
type Decision string

const (
	DecisionCompatible                     Decision = "compatible"
	DecisionDegraded                       Decision = "degraded"
	DecisionClientTooOld                   Decision = "client-too-old"
	DecisionServerTooOld                   Decision = "server-too-old"
	DecisionServerInstallationInconsistent Decision = "server-installation-inconsistent"
)

// ReasonCode is the closed compatibility-reason vocabulary.
type ReasonCode string

const (
	ReasonUnknownSurface         ReasonCode = "unknown-surface"
	ReasonUnknownBuild           ReasonCode = "unknown-build"
	ReasonRevokedBuild           ReasonCode = "revoked-build"
	ReasonUnverifiedDistribution ReasonCode = "unverified-distribution"
	ReasonEmbeddedUISkew         ReasonCode = "embedded-ui-skew"
	ReasonCoreTooOld             ReasonCode = "core-revision-too-old"
	ReasonCoreTooNew             ReasonCode = "core-revision-too-new"
	ReasonRenderingTooOld        ReasonCode = "rendering-revision-too-old"
	ReasonRenderingTooNew        ReasonCode = "rendering-revision-too-new"
	ReasonMissingRenderingStamp  ReasonCode = "missing-rendering-stamp"
	ReasonMissingVocabulary      ReasonCode = "missing-vocabulary"
	ReasonUnsupportedAPIMajor    ReasonCode = "unsupported-api-major"
	ReasonUnsupportedCapability  ReasonCode = "unknown-capability"
)

const (
	// EmbeddedUIContractRevision is the contract revision compiled into this binary.
	EmbeddedUIContractRevision Revision = 1
	embeddedUIBuildID                   = "embedded-core-web-ui-v1"
	embeddedUIRenderingStamp            = "core-web-ui-v1"
	embeddedUIBuildSequence             = 1
)

// ClientContract describes the compatibility facts supplied by a client.
type ClientContract struct {
	Surface                   string      `json:"surface"`
	AppVersion                string      `json:"appVersion"`
	BuildID                   string      `json:"buildID"`
	ArtifactHash              string      `json:"artifactHash"`
	CoreContractRevRange      [2]Revision `json:"coreContractRevRange"`
	RenderingContractRevRange [2]Revision `json:"renderingContractRevRange"`
	RenderingStamps           []string    `json:"renderingStamps"`
	FixedVocabularies         []string    `json:"fixedVocabularies"`
	RequestedCapabilities     []string    `json:"requestedCapabilities"`
	DeveloperAPIMajor         int         `json:"developerAPIMajor"`
}

// Policy is the server's compatibility policy.
type Policy struct {
	SupportedCoreRevisions      [2]Revision
	SupportedRenderingRevisions [2]Revision
	RequiredRenderingStamps     []string
	RequiredVocabularies        []string
	KnownCapabilities           map[string]bool
	SupportedDeveloperAPIMajor  int
	RevokedBuilds               map[string]bool
	// Builds is the server-known record of legitimate client builds. It is
	// authoritative: a build absent from it has no provenance and is refused.
	Builds             BuildCatalog
	EmbeddedUIRevision Revision
	Generation         int
}

// Result is a compatibility decision and its closed diagnostic values.
type Result struct {
	Decision             Decision   `json:"decision"`
	ReasonCode           ReasonCode `json:"reasonCode,omitempty"`
	DisabledCapabilities []string   `json:"disabledCapabilities,omitempty"`
}

// Servable reports whether normal requests may use this decision.
func (r Result) Servable() bool {
	return r.Decision == DecisionCompatible || r.Decision == DecisionDegraded
}

// EmbeddedUIContract returns the server-derived contract for the embedded UI.
func EmbeddedUIContract() ClientContract {
	return ClientContract{
		Surface:                   string(SurfaceCoreWebUI),
		AppVersion:                "embedded",
		BuildID:                   embeddedUIBuildID,
		ArtifactHash:              embeddedUIArtifactHash(),
		CoreContractRevRange:      [2]Revision{EmbeddedUIContractRevision, EmbeddedUIContractRevision},
		RenderingContractRevRange: [2]Revision{EmbeddedUIContractRevision, EmbeddedUIContractRevision},
		RenderingStamps:           []string{embeddedUIRenderingStamp},
		FixedVocabularies:         []string{},
		RequestedCapabilities:     []string{},
	}
}

// NormalizeClientContract canonicalizes set-valued contract fields and the
// identity fields provenance compares, so trailing space or upper-case hex
// cannot dodge a revocation entry or a catalog digest comparison.
func NormalizeClientContract(contract ClientContract) ClientContract {
	contract.Surface = strings.TrimSpace(contract.Surface)
	contract.BuildID = strings.TrimSpace(contract.BuildID)
	contract.ArtifactHash = strings.ToLower(strings.TrimSpace(contract.ArtifactHash))
	contract.RenderingStamps = normalizedStrings(contract.RenderingStamps)
	contract.FixedVocabularies = normalizedStrings(contract.FixedVocabularies)
	contract.RequestedCapabilities = normalizedStrings(contract.RequestedCapabilities)
	return contract
}

// Evaluate applies the compatibility policy in fail-closed guard order. The
// enrollment carries the server-derived caller identity and distribution class;
// no compatible or degraded decision is reachable without verified build
// provenance for that enrollment.
func Evaluate(contract ClientContract, enrollment Enrollment, policy Policy) Result {
	contract = NormalizeClientContract(contract)
	distribution := enrollment.Distribution
	if _, ok := knownSurfaces[Surface(contract.Surface)]; !ok {
		return refusal(DecisionClientTooOld, ReasonUnknownSurface)
	}
	if contract.BuildID == "" {
		return refusal(DecisionClientTooOld, ReasonUnknownBuild)
	}
	if policy.RevokedBuilds[contract.BuildID] {
		return refusal(DecisionClientTooOld, ReasonRevokedBuild)
	}
	if distribution != DistributionDirect && distribution != DistributionAppStore {
		return refusal(DecisionClientTooOld, ReasonUnverifiedDistribution)
	}
	if VerifyBuildProvenance(contract, enrollment, policy.Builds) != ProvenanceVerified {
		return refusal(DecisionClientTooOld, ReasonUnknownBuild)
	}
	if !validRevisionRange(contract.CoreContractRevRange) || !validRevisionRange(contract.RenderingContractRevRange) {
		return refusal(DecisionClientTooOld, ReasonUnknownBuild)
	}
	if Surface(contract.Surface) == SurfaceCoreWebUI &&
		!rangeContains(contract.CoreContractRevRange, policy.EmbeddedUIRevision) {
		return refusal(DecisionServerInstallationInconsistent, ReasonEmbeddedUISkew)
	}
	if contract.CoreContractRevRange[1] < policy.SupportedCoreRevisions[0] {
		return refusal(DecisionClientTooOld, ReasonCoreTooOld)
	}
	if contract.CoreContractRevRange[0] > policy.SupportedCoreRevisions[1] {
		return refusal(DecisionServerTooOld, ReasonCoreTooNew)
	}
	if contract.RenderingContractRevRange[1] < policy.SupportedRenderingRevisions[0] {
		return refusal(DecisionClientTooOld, ReasonRenderingTooOld)
	}
	if contract.RenderingContractRevRange[0] > policy.SupportedRenderingRevisions[1] {
		return refusal(DecisionServerTooOld, ReasonRenderingTooNew)
	}
	if !containsAll(contract.RenderingStamps, policy.RequiredRenderingStamps) {
		return refusal(DecisionClientTooOld, ReasonMissingRenderingStamp)
	}
	if !containsAll(contract.FixedVocabularies, policy.RequiredVocabularies) {
		return refusal(DecisionClientTooOld, ReasonMissingVocabulary)
	}
	if contract.DeveloperAPIMajor != 0 &&
		contract.DeveloperAPIMajor != policy.SupportedDeveloperAPIMajor {
		return refusal(DecisionClientTooOld, ReasonUnsupportedAPIMajor)
	}
	if hasUnknownCapability(contract.RequestedCapabilities, policy.KnownCapabilities) {
		return Result{
			Decision:   DecisionDegraded,
			ReasonCode: ReasonUnsupportedCapability,
		}
	}
	return Result{Decision: DecisionCompatible}
}

// ContractHash returns a stable hash of the normalized contract and derived distribution.
func ContractHash(contract ClientContract, distribution Distribution) string {
	payload := struct {
		Contract     ClientContract `json:"contract"`
		Distribution Distribution   `json:"distribution"`
	}{
		Contract:     NormalizeClientContract(contract),
		Distribution: distribution,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func refusal(decision Decision, reason ReasonCode) Result {
	return Result{Decision: decision, ReasonCode: reason}
}

func rangeContains(revisions [2]Revision, revision Revision) bool {
	return revisions[0] <= revision && revision <= revisions[1]
}

func validRevisionRange(revisions [2]Revision) bool {
	return revisions[0] <= revisions[1]
}

func containsAll(have, required []string) bool {
	members := make(map[string]struct{}, len(have))
	for _, value := range have {
		members[value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := members[value]; !ok {
			return false
		}
	}
	return true
}

func hasUnknownCapability(requested []string, known map[string]bool) bool {
	for _, capability := range requested {
		if !known[capability] {
			return true
		}
	}
	return false
}

func normalizedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	write := 0
	for _, value := range out {
		if write != 0 && out[write-1] == value {
			continue
		}
		out[write] = value
		write++
	}
	return out[:write]
}
