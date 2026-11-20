package app

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

func TestActiveEntityBrowseRulesKeepsOnlyDeclaredRules(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	want := []EntityBrowseSource{
		{Relation: "album-artist", FromKind: "album"},
		{Relation: "appears-on", ExcludeMediaRelation: "album-track"},
	}
	rules := []EntityBrowseRule{
		{PluginID: "inactive", Kind: "artist", Sources: want},
		{PluginID: testHydrationPluginID, Kind: "artist", Sources: want},
		{PluginID: testHydrationPluginID, Kind: "artist", Sources: []EntityBrowseSource{
			{Relation: "appears-on", ExcludeMediaRelation: "album-artist"},
		}},
		{PluginID: testHydrationPluginID, Kind: "album", Sources: []EntityBrowseSource{
			{Relation: "album-artist", FromKind: "album"},
		}},
	}
	got := activeEntityBrowseRules(registry, rules, nil)
	if kinds := got[testHydrationPluginID]; len(got) != 1 || len(kinds) != 1 || !reflect.DeepEqual(kinds["artist"], want) {
		t.Fatalf("active browse rules = %+v, want only artist sources %+v", got, want)
	}
}
