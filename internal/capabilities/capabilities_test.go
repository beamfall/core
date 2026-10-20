//go:build !beamfall_no_powertools && !beamfall_appstore

package capabilities

import "testing"

func TestCurrentListsDefaultCapabilities(t *testing.T) {
	got := Current().Capabilities
	want := []string{"conversion", "rename"}
	if len(got) != len(want) {
		t.Fatalf("capabilities = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("capabilities = %#v, want %#v", got, want)
		}
	}
}
