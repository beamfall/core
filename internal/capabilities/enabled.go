//go:build !beamfall_no_powertools && !beamfall_appstore

package capabilities

// enabled is the embedded Workflow (operator-flippable) capability set.
// These default to enabled and may be flipped at runtime via the
// Admin-Session Console/Manager toggle.
var enabled = []Descriptor{
	{ID: "conversion", Kind: KindWorkflow, Version: CapabilityVersion, DefaultEnabled: true},
	{ID: "rename", Kind: KindWorkflow, Version: CapabilityVersion, DefaultEnabled: true},
}

// probeCapabilities is the embedded probe-derived (auto-detected, never
// operator-flipped) capability set. Each runs Probe() to detect
// host presence; an absent probe degrades observably to
// `enabled:false, reason:probe-absent` and the Console/Manager omits its toggle.
var probeCapabilities = []Descriptor{
	{ID: "share-mount", Kind: KindProbe, Version: CapabilityVersion, Probe: probeShareMount},
}

// inferenceCapabilities is the embedded local AI enrichment Integration Plugin
// capability set. Local availability is model-derived: with no model
// present it degrades observably to `enabled:false, reason:model-absent` (a
// discoverable "download a model to enable" state), and each entry carries a
// manifest Version for client discovery. Gated by the same
// compile-time axis as the other sets, so a `beamfall_no_powertools` binary
// advertises no inference capability.
var inferenceCapabilities = []Descriptor{
	{ID: "inference", Kind: KindInference, Version: InferenceCapabilityVersion, ModelPresent: probeModelPresent},
}
