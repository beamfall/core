package app

import (
	"reflect"
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

// Only rules an activated bundle can back reach the artwork sweep: another
// plugin's rules and rules naming an undeclared kind or relation are dropped,
// and the rest keep their declared order.
func TestActiveEntityPosterRulesKeepsOnlyDeclaredRules(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	album := EntityPosterRule{PluginID: testHydrationPluginID, Kind: "album", Sources: []EntityPosterSource{{Relation: "album-track"}}}
	artist := EntityPosterRule{PluginID: testHydrationPluginID, Kind: "artist", Sources: []EntityPosterSource{
		{Relation: "album-artist", FromKind: "album"},
		{Relation: "appears-on"},
	}}
	rules := []EntityPosterRule{
		album,
		{PluginID: "not-activated", Kind: "album", Sources: album.Sources},
		{PluginID: testHydrationPluginID, Kind: "playlist", Sources: album.Sources},
		{PluginID: testHydrationPluginID, Kind: "album", Sources: []EntityPosterSource{{Relation: "appears-on"}}},
		{PluginID: testHydrationPluginID, Kind: "genre", Sources: []EntityPosterSource{{Relation: "album-artist", FromKind: "album"}}},
		{PluginID: testHydrationPluginID, Kind: "album"},
		artist,
	}

	got := activeEntityPosterRules(registry, rules, nil)

	if want := []EntityPosterRule{album, artist}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active rules = %+v, want %+v", got, want)
	}
	if got := activeEntityPosterRules(nil, rules, nil); got != nil {
		t.Fatalf("rules without a registry = %+v, want none", got)
	}
}

// A category kind with no declared rule gets a mosaic rule over what it
// groups; a declared rule for the kind suppresses the derived one, and
// other archetypes get none.
func TestCategoryKindsDeriveMosaicPosterRules(t *testing.T) {
	registry, err := plugin.NewRegistry([]plugin.Bundle{testHydrationBundle()})
	if err != nil {
		t.Fatal(err)
	}
	album := EntityPosterRule{PluginID: testHydrationPluginID, Kind: "album", Sources: []EntityPosterSource{{Relation: "album-track"}}}

	got := withCategoryPosterRules(registry, []EntityPosterRule{album})

	derived := EntityPosterRule{PluginID: testHydrationPluginID, Kind: "genre", Mosaic: true, Sources: []EntityPosterSource{{Relation: "classifies"}}}
	if want := []EntityPosterRule{album, derived}; !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %+v, want %+v", got, want)
	}
	declared := EntityPosterRule{PluginID: testHydrationPluginID, Kind: "genre", Sources: []EntityPosterSource{{Relation: "classifies"}}}
	if got := withCategoryPosterRules(registry, []EntityPosterRule{declared}); !reflect.DeepEqual(got, []EntityPosterRule{declared}) {
		t.Fatalf("rules with a declared category rule = %+v, want only it", got)
	}
	if got := withCategoryPosterRules(nil, nil); got != nil {
		t.Fatalf("rules without a registry = %+v, want none", got)
	}
}
