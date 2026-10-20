package capabilities

import "sort"

// Capability kinds. A Workflow capability's enabled state is set
// by config and flippable at runtime via the Console/Manager (an audited,
// attributed, Admin-Session-gated mutation). A Probe capability is auto-detected
// from the host and is NEVER operator-flipped: when its probe is absent the
// capability degrades observably (`enabled:false, reason:probe-absent`) rather
// than silently disappearing, so a degraded binary is never indistinguishable
// from a misconfigured one.
const (
	// KindWorkflow is an operator-flippable Workflow Plugin capability
	// (conversion, rename). Its default enabled state comes from config; the
	// Console/Manager toggle overrides it at runtime.
	KindWorkflow = "workflow"
	// KindProbe is a probe-derived capability (driver-matrix backends, fswatch,
	// share-mount). It is auto-detected, never operator-flipped.
	KindProbe = "probe"
	// KindInference is the local AI enrichment Integration Plugin capability
	//. Like a probe capability its local availability is auto-detected
	// — but from *model* presence, not a host probe — so when no model is present
	// it degrades observably to `enabled:false, reason:model-absent` (a
	// discoverable "download a model to enable" state, never indistinguishable
	// from a broken install). It carries a manifest Version for client discovery
	// (ADR-0124 §7: ID + machine-comparable version).
	KindInference = "inference"
)

// ReasonProbeAbsent is the observable a probe-derived capability reports when
// its host probe fails: the capability degrades gracefully and
// the Console/Manager omits its toggle.
const ReasonProbeAbsent = "probe-absent"

// ReasonModelAbsent is the observable the inference capability reports when no
// model is present (ADR-0127 §5 / Acceptance 9): a discoverable disabled state
// (`enabled:false, reason:model-absent`) that a client distinguishes from a
// broken install, never a silently omitted capability.
const ReasonModelAbsent = "model-absent"

// InferenceCapabilityVersion is the inference capability's machine-comparable
// manifest version (ADR-0124 §7: a client can do `>=` without parsing build
// tags; ADR-0127 §5 manifest entry names ID + version). Bump it when the
// inference capability contract changes in a client-observable way.
const InferenceCapabilityVersion = CapabilityVersion

// Descriptor describes one compiled-in capability and how its runtime enabled
// state is decided. Workflow descriptors are operator-flippable (DefaultEnabled
// seeds the state, the Console override decides the rest); Probe descriptors run
// Probe() to auto-detect presence and are never operator-flipped.
type Descriptor struct {
	// ID is the stable capability identifier surfaced by `beamfall capabilities`.
	ID string
	// Kind is KindWorkflow, KindProbe, or KindInference.
	Kind string
	// DefaultEnabled is the Workflow capability's enabled state when no config or
	// Console override is present. Ignored for Probe capabilities.
	DefaultEnabled bool
	// Probe auto-detects a Probe capability's host presence. nil for Workflow
	// capabilities. A nil result from a non-nil Probe means absent.
	Probe func() bool
	// Version is the positive integer contract version surfaced by `beamfall
	// capabilities` for machine-comparable client discovery.
	Version uint
	// ModelPresent auto-detects an Inference capability's model presence. nil for
	// non-inference capabilities. A false result degrades the capability to
	// `enabled:false, reason:model-absent`.
	ModelPresent func() bool
}

// State is the resolved runtime state of one capability. Reason is
// empty when Enabled is true or when a Workflow capability was operator-disabled;
// it is ReasonProbeAbsent when a Probe capability's host probe failed.
type State struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	// Version is the positive integer manifest version used by every capability
	// identity.
	Version uint `json:"version"`
}

// descriptors is the compiled-in capability set. `enabled` (enabled.go /
// disabled.go — the App Store firewall's one compile-time axis via
// beamfall_no_powertools) is the embedded Workflow set `Current()` reports;
// `probeCapabilities` carries the auto-detected probe caps. Both are gated by the
// same compile-time axis, so a `beamfall_no_powertools` binary advertises neither.
func descriptors() []Descriptor {
	out := make([]Descriptor, 0, len(enabled)+len(probeCapabilities)+len(inferenceCapabilities))
	out = append(out, enabled...)
	out = append(out, probeCapabilities...)
	out = append(out, inferenceCapabilities...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Descriptors returns the compiled-in capability descriptors, sorted by ID.
func Descriptors() []Descriptor { return descriptors() }

// WorkflowIDs returns the sorted IDs of compiled-in Workflow (operator-flippable)
// capabilities. The Console/Manager toggle surface and the store's capability
// override vocabulary key on this set, so an unknown id can never be flipped.
func WorkflowIDs() []string {
	var ids []string
	for _, d := range descriptors() {
		if d.Kind == KindWorkflow {
			ids = append(ids, d.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// Resolve computes the runtime State of every compiled-in capability. overrides
// carries the operator's Console toggle for Workflow capabilities (id -> enabled,
// absent = use DefaultEnabled); it is ignored for Probe capabilities, which are
// auto-detected and degrade to `enabled:false, reason:probe-absent` when their
// probe fails.
func Resolve(overrides map[string]bool) []State {
	descs := descriptors()
	states := make([]State, 0, len(descs))
	for _, d := range descs {
		state := State{ID: d.ID, Kind: d.Kind, Version: d.Version}
		switch d.Kind {
		case KindProbe:
			present := d.Probe == nil || d.Probe()
			state.Enabled = present
			if !present {
				state.Reason = ReasonProbeAbsent
			}
		case KindInference:
			// Local availability is model-derived: an absent model degrades the
			// capability observably rather than omitting it. Model
			// presence is auto-detected, so an operator override cannot force it on.
			present := d.ModelPresent != nil && d.ModelPresent()
			state.Enabled = present
			if !present {
				state.Reason = ReasonModelAbsent
			}
		default:
			state.Enabled = d.DefaultEnabled
			if override, ok := overrides[d.ID]; ok {
				state.Enabled = override
			}
		}
		states = append(states, state)
	}
	return states
}

// EnabledIDs returns the sorted IDs of capabilities resolved as enabled, given
// the operator's Workflow overrides. This is the runtime-state capability list
// `beamfall capabilities` reports (ADR-0124 §5: a flipped capability is
// immediately visible).
func EnabledIDs(overrides map[string]bool) []string {
	var ids []string
	for _, s := range Resolve(overrides) {
		if s.Enabled {
			ids = append(ids, s.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
