//go:build beamfall_no_powertools || beamfall_appstore

package capabilities

// enabled and probeCapabilities are empty under the App Store firewall's
// compile-time axis (beamfall_no_powertools): the binary advertises no embedded
// Workflow or probe capabilities.
var enabled []Descriptor

var probeCapabilities []Descriptor

var inferenceCapabilities []Descriptor
