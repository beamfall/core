package nodehostd

import "github.com/beamfall/core/internal/nodehost"

// Supported node-host contract state for this build. These are the single
// production source of the ADR-0129 §2 / ADR-0063 compatibility inputs, used
// identically by enrollment, every session open, and the transport tests — a
// session must never run against a zero/nil policy (which would fail every
// revision check closed). They mirror internal/conformance's enrollmentPolicy
// so the wired transport and the conformance corpus evaluate the same handshake.
const (
	// contractRevision is the sole node-host contract revision this build
	// speaks. New operations arrive only via a new revision.
	contractRevision nodehost.Revision = 1
	// policyGeneration is bumped when the policy below changes, so an
	// already-enrolled node's decision is re-evaluated against the current one.
	policyGeneration = 1
)

// ProductionPolicy is the compatibility policy the wired transport evaluates
// nodes against. Vocabularies: "core" is required to serve at all; "transcode"
// is required for FULL (non-degraded) serving.
func ProductionPolicy() nodehost.CompatibilityPolicy {
	return nodehost.CompatibilityPolicy{
		SupportedRevisions:      map[nodehost.Revision]bool{contractRevision: true},
		RequiredVocabularies:    []string{"core"},
		FullServiceVocabularies: []string{"transcode"},
		RevokedBuilds:           map[string]bool{},
		Generation:              policyGeneration,
	}
}
