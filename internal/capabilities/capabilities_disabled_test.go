//go:build beamfall_no_powertools || beamfall_appstore

package capabilities

import "testing"

func TestNoPowertoolsBuildOmitsCapabilities(t *testing.T) {
	if got := Current().Capabilities; len(got) != 0 {
		t.Fatalf("capabilities = %#v, want none", got)
	}
}
