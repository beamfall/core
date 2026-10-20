package plugin

import (
	"encoding/json"
	"testing"
)

func TestDerivePluginProvenanceMatrix(t *testing.T) {
	states := []PluginTrustState{
		PluginTrusted,
		PluginTampered,
		PluginRevoked,
		PluginUnrecognized,
		PluginUnsigned,
	}
	tests := []struct {
		name string
		path pluginLoadPath
		want map[PluginTrustState]PluginProvenance
	}{
		{
			name: "compiled built-in",
			path: loadPathBuiltin,
			want: map[PluginTrustState]PluginProvenance{
				PluginTrusted: ProvenanceFirstParty, PluginTampered: ProvenanceFirstParty,
				PluginRevoked: ProvenanceFirstParty, PluginUnrecognized: ProvenanceFirstParty,
				PluginUnsigned: ProvenanceFirstParty,
			},
		},
		{
			name: "first-party registry",
			path: loadPathRegistryFirstParty,
			want: map[PluginTrustState]PluginProvenance{
				PluginTrusted: ProvenanceFirstParty, PluginTampered: ProvenanceSideloaded,
				PluginRevoked: ProvenanceFirstParty, PluginUnrecognized: ProvenanceSideloaded,
				PluginUnsigned: ProvenanceSideloaded,
			},
		},
		{
			name: "third-party registry",
			path: loadPathRegistryThirdParty,
			want: map[PluginTrustState]PluginProvenance{
				PluginTrusted: ProvenanceThirdParty, PluginTampered: ProvenanceSideloaded,
				PluginRevoked: ProvenanceThirdParty, PluginUnrecognized: ProvenanceSideloaded,
				PluginUnsigned: ProvenanceSideloaded,
			},
		},
		{
			name: "sideloaded",
			path: loadPathSideloaded,
			want: map[PluginTrustState]PluginProvenance{
				PluginTrusted: ProvenanceSideloaded, PluginTampered: ProvenanceSideloaded,
				PluginRevoked: ProvenanceSideloaded, PluginUnrecognized: ProvenanceSideloaded,
				PluginUnsigned: ProvenanceSideloaded,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, state := range states {
				if got := derivePluginProvenance(test.path, state); got != test.want[state] {
					t.Errorf("state %s provenance = %q, want %q", state, got, test.want[state])
				}
			}
		})
	}
}

func TestRegistryStandingComesFromLoadPath(t *testing.T) {
	bundle := validBundle()
	bundle.DisplayName = L("Beamfall Official ✓")

	sideloaded, err := NewRegistry([]Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginStanding(t, sideloaded, bundle.ID, PluginStanding{
		Provenance: ProvenanceSideloaded,
		TrustState: PluginUnsigned,
	})

	builtin, err := NewBuiltinRegistry([]Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginStanding(t, builtin, bundle.ID, PluginStanding{
		Provenance: ProvenanceFirstParty,
		TrustState: PluginUnsigned,
	})
}

func TestRegistryIgnoresManifestDeclaredStanding(t *testing.T) {
	declared := struct {
		Bundle
		Provenance string `json:"provenance"`
		Authority  string `json:"authority"`
	}{
		Bundle:     validBundle(),
		Provenance: string(ProvenanceFirstParty),
		Authority:  "beamfall-root",
	}
	raw, err := json.Marshal(declared)
	if err != nil {
		t.Fatal(err)
	}
	var bundle Bundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}

	registry, err := NewRegistry([]Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	assertPluginStanding(t, registry, bundle.ID, PluginStanding{
		Provenance: ProvenanceSideloaded,
		TrustState: PluginUnsigned,
	})
}

func assertPluginStanding(t *testing.T, registry *Registry, id ID, want PluginStanding) {
	t.Helper()
	got, ok := registry.Standing(id)
	if !ok {
		t.Fatalf("standing for %q is missing", id)
	}
	if got != want {
		t.Fatalf("standing for %q = %#v, want %#v", id, got, want)
	}
}
