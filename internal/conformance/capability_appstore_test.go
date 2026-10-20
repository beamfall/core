//go:build beamfall_appstore

package conformance

import (
	"testing"

	"github.com/beamfall/core/internal/capabilities"
	"github.com/beamfall/core/internal/graph"
)

// TestAppStoreSuppressesCapabilityControlAPI is the invariant-8 API-suppression
// conformance for the capability runtime-gating control surface:
// on an App Store binary the control surface is suppressed BOTH UI and API —
// because Workflow Plugins are compiled IN, suppressing only the toggle UI would
// leave a reachable runtime endpoint. This test runs ONLY under the
// beamfall_appstore build tag and proves the control API is neither advertised
// (absent from the schema manifest) nor reachable (rejected as an unknown
// top-level field). It is a TEST of the API, not merely UI suppression.
func TestAppStoreSuppressesCapabilityControlAPI(t *testing.T) {
	suppressed := []string{"capabilitystates", "setcapabilityenabled", "qbittorrentingest", "setqbittorrentingest"}

	// Not advertised: the field is absent from the dispatcher's schema manifest.
	for _, field := range graph.SchemaManifest() {
		for _, name := range suppressed {
			if field.Field == name {
				t.Fatalf("App Store binary advertises capability-control field %q in the schema manifest — must be suppressed (invariant 8)", name)
			}
		}
	}

	// Not reachable: a request naming the control field is rejected as an unknown
	// top-level field rather than dispatched. UnknownTopLevelFields reports the
	// dispatcher's own verdict (nothing in the registries recognizes the field).
	for _, name := range suppressed {
		unknown := graph.UnknownTopLevelFields(map[string]bool{name: true})
		if len(unknown) != 1 || unknown[0] != name {
			t.Fatalf("App Store binary recognizes capability-control field %q (unknown=%v) — must be unreachable (invariant 8)", name, unknown)
		}
	}
}

func TestAppStoreOmitsPowertoolsCapabilities(t *testing.T) {
	report := capabilities.Current()
	if len(report.Capabilities) != 0 {
		t.Fatalf("App Store binary capabilities = %#v, want none", report.Capabilities)
	}
	if len(report.CapabilityStates) != 0 {
		t.Fatalf("App Store binary capability states = %#v, want none", report.CapabilityStates)
	}
}
