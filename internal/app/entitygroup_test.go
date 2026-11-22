package app

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/plugin"
)

// TestActiveEntityGroupRulesKeepsOnlyDeclaredTextFields pins CORE-2769: a
// group rule survives only for an active plugin whose kind declares the field
// as text.
func TestActiveEntityGroupRulesKeepsOnlyDeclaredTextFields(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	groups := []EntityGroupValue{{Value: "a", Label: plugin.L("As")}}
	rules := []EntityGroupRule{
		{PluginID: "inactive", Kind: "album", Field: "musicbrainz-release-id", Groups: groups},
		{PluginID: testHydrationPluginID, Kind: "album", Field: "release-date", Groups: groups},
		{PluginID: testHydrationPluginID, Kind: "album", Field: "undeclared", Groups: groups},
		{PluginID: testHydrationPluginID, Kind: "undeclared", Field: "musicbrainz-release-id", Groups: groups},
		{PluginID: testHydrationPluginID, Kind: "album", Field: "musicbrainz-release-id", Groups: groups},
	}
	got := activeEntityGroupRules(registry, rules, nil)
	want := map[plugin.ID]map[string]graph.EntityGroupRule{
		testHydrationPluginID: {"album": {Field: "musicbrainz-release-id", Groups: groups}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active group rules = %+v, want %+v", got, want)
	}
}
