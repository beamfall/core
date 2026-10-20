package conformance

import (
	"testing"

	"github.com/beamfall/core/internal/nodehost"
)

// TestOriginNodeCompatDecision is the conformance spec for the ADR-0063 NODE-0
// amendment (ratified 2026-06-28) that adds the `origin-node` Client Contract
// surface and the node↔main version-skew rules driving ADR-0123 (Origin Nodes).
//
// CONF-1: this test now calls the real nodehost.EvaluateCompatibility production
// handshake in addition to the local spec stubs, so any drift between the spec
// and the production implementation is caught here. The spec stubs (below) remain
// as the normative contract reference; the production calls exercise the impl.
//
//   - a newer node binary  -> server-too-old -> refuse-serving / Unavailable
//   - an older node binary  -> client-too-old -> refuse-serving / Unavailable
//   - a compatible node     -> compatible     -> serving
//   - the `origin-node` surface is ACCEPTED by the closed vocabulary (NOT
//     failed-closed as an unknown surface).
//
// Critically: a node that goes server-too-old/client-too-old must drop to
// refuse-serving and surface `Origin-Node Unavailable`, NEVER an update-required
// screen (origin-node is a non-UX infrastructure surface class) and NEVER a
// silent mis-parse.

// compatDecision is the existing ADR-0063 Compatibility Decision vocabulary.
type compatDecision string

const (
	decCompatible       compatDecision = "compatible"
	decDegraded         compatDecision = "degraded"
	decServerTooOld     compatDecision = "server-too-old"
	decClientTooOld     compatDecision = "client-too-old"
	decInstallInconsist compatDecision = "server-installation-inconsistent"
)

// nodeBehavior is the node-appropriate resolution of a decision for the
// `origin-node` surface (per the NODE-0 amendment): node skew never renders a
// human UX screen; it serves, narrows, or refuses-serving.
type nodeBehavior string

const (
	behServing       nodeBehavior = "serving"        // normal node participation
	behNarrowed      nodeBehavior = "narrowed"       // degraded: only proven-safe surfaces
	behRefuseServing nodeBehavior = "refuse-serving" // stop serving; surface Origin-Node Unavailable
	behUpdateScreen  nodeBehavior = "update-screen"  // FORBIDDEN for origin-node (non-UX class)
)

// originNodeSurfaceAccepted models paragraph-3's closed surface vocabulary AFTER
// the NODE-0 amendment: `origin-node` is a recognized fixed value; anything else
// the spec does not know fails closed.
func originNodeSurfaceAccepted(surface string) bool {
	closed := map[string]bool{
		"core-web-ui":          true,
		"macos-manager":        true,
		"ios-viewer":           true,
		"ipad-manager":         true,
		"tvos-viewer":          true,
		"android-viewer":       true,
		"developer-api-client": true,
		"origin-node":          true, // added by NODE-0
	}
	return closed[surface]
}

// originNodeDecisionSpec maps a (node binary vs main supported-contract) skew to
// the documented decision. nodeNewer => the node binary is newer than the main
// server's supported contract (server-too-old); mainCannotServe => the main can
// no longer safely serve this node (client-too-old).
func originNodeDecisionSpec(nodeNewer, mainCannotServe bool) compatDecision {
	switch {
	case nodeNewer:
		return decServerTooOld
	case mainCannotServe:
		return decClientTooOld
	default:
		return decCompatible
	}
}

// originNodeBehaviorSpec resolves a decision to node behavior for the
// `origin-node` surface. The amendment forbids an update screen for any node
// decision and forces refuse-serving for both skew decisions.
func originNodeBehaviorSpec(d compatDecision) nodeBehavior {
	switch d {
	case decCompatible:
		return behServing
	case decDegraded:
		return behNarrowed
	case decServerTooOld, decClientTooOld:
		return behRefuseServing
	default:
		return behRefuseServing // fail-closed on anything unexpected; never guess
	}
}

func TestOriginNodeSurfaceAccepted(t *testing.T) {
	if !originNodeSurfaceAccepted("origin-node") {
		t.Fatal("origin-node surface must be ACCEPTED by the closed vocabulary after NODE-0, not failed-closed as unknown")
	}
	if originNodeSurfaceAccepted("rogue-surface") {
		t.Fatal("an unknown surface must still fail closed (unrecognized => not accepted)")
	}
}

// productionPolicy is a minimal CompatibilityPolicy for exercising
// nodehost.EvaluateCompatibility in CONF-1. Revision 1 is the supported set;
// revision 2 represents a future-only node (server-too-old); revision 0 an
// old-only node (client-too-old). No required/full-service vocabularies so the
// decision is driven purely by revision-skew and the fixed surface/build checks.
var productionPolicy = nodehost.CompatibilityPolicy{
	SupportedRevisions: map[nodehost.Revision]bool{1: true},
}

// compatibleContract is a minimal ClientContract that passes all non-revision
// checks so that the revision-skew cases can be exercised in isolation.
func compatibleContract(revMin, revMax nodehost.Revision) nodehost.ClientContract {
	return nodehost.ClientContract{
		Surface:              "origin-node",
		BuildID:              "test-build-conformance",
		CoreContractRevRange: [2]nodehost.Revision{revMin, revMax},
		DistributionVerified: true,
	}
}

// decisionString bridges the nodehost.Decision values to the local
// compatDecision vocabulary so the two can be compared in a single table.
func decisionString(d nodehost.Decision) compatDecision {
	return compatDecision(d)
}

func TestOriginNodeCompatDecision(t *testing.T) {
	cases := []struct {
		name            string
		nodeNewer       bool
		mainCannotServe bool
		wantDecision    compatDecision
		wantBehavior    nodeBehavior
		// productionRevMin/Max drive the CONF-1 production call. The revision
		// range encodes the same skew scenario as nodeNewer/mainCannotServe:
		//   nodeNewer=true   => node min (2) > main max (1)  => server-too-old
		//   mainCannotServe  => node max (0) < main min (1)  => client-too-old
		//   compatible       => node range [1,1] overlaps {1} => compatible
		productionRevMin nodehost.Revision
		productionRevMax nodehost.Revision
	}{
		{
			name:             "newer node -> server-too-old -> refuse-serving",
			nodeNewer:        true,
			wantDecision:     decServerTooOld,
			wantBehavior:     behRefuseServing,
			productionRevMin: 2,
			productionRevMax: 2,
		},
		{
			name:             "older node -> client-too-old -> refuse-serving",
			mainCannotServe:  true,
			wantDecision:     decClientTooOld,
			wantBehavior:     behRefuseServing,
			productionRevMin: 0,
			productionRevMax: 0,
		},
		{
			name:             "compatible node -> serving",
			wantDecision:     decCompatible,
			wantBehavior:     behServing,
			productionRevMin: 1,
			productionRevMax: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Spec path: assert the documented rules via the local spec stubs.
			gotDecision := originNodeDecisionSpec(tc.nodeNewer, tc.mainCannotServe)
			if gotDecision != tc.wantDecision {
				t.Fatalf("spec decision = %q, want %q", gotDecision, tc.wantDecision)
			}
			gotBehavior := originNodeBehaviorSpec(gotDecision)
			if gotBehavior != tc.wantBehavior {
				t.Fatalf("spec behavior = %q, want %q", gotBehavior, tc.wantBehavior)
			}
			// A node decision must NEVER render a human update screen — origin-node
			// is a non-UX infrastructure surface class.
			if gotBehavior == behUpdateScreen {
				t.Fatalf("origin-node decision %q resolved to an update-screen — forbidden for a non-UX surface", gotDecision)
			}

			// CONF-1: production path — call nodehost.EvaluateCompatibility and
			// assert it agrees with the spec. This exercises the real ADR-0063
			// Compatibility Handshake implementation, not just local stubs.
			contract := compatibleContract(tc.productionRevMin, tc.productionRevMax)
			prodDecision := decisionString(nodehost.EvaluateCompatibility(contract, productionPolicy))
			if prodDecision != tc.wantDecision {
				t.Fatalf("production EvaluateCompatibility = %q, want %q (spec and impl disagree)", prodDecision, tc.wantDecision)
			}
		})
	}
}

func TestOriginNodeDegradedNarrows(t *testing.T) {
	if got := originNodeBehaviorSpec(decDegraded); got != behNarrowed {
		t.Fatalf("degraded node behavior = %q, want %q (narrow to proven-safe only)", got, behNarrowed)
	}
}
