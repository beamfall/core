package identify

import (
	"maps"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// chainBundle declares a two-level container chain (part holds Media and
// nests into volume), a container kind outside every chain (shelf) and a
// non-container kind pointing at Media (maker).
func chainBundle() plugin.Bundle {
	return plugin.Bundle{EntityKinds: []plugin.EntityKind{
		{Name: "part", Archetype: "container", Relations: []plugin.RelationSpec{
			{Name: "holds", ToKind: plugin.MediaKindName, Cardinality: "one"},
			{Name: "part-of", ToKind: "volume"},
		}},
		{Name: "volume", Archetype: "container"},
		{Name: "shelf", Archetype: "container"},
		{Name: "maker", Archetype: "person", Relations: []plugin.RelationSpec{
			{Name: "made", ToKind: plugin.MediaKindName, Cardinality: "one"},
		}},
	}}
}

func TestContainerRelationsIndexesHoldersAndNesting(t *testing.T) {
	index := ContainerRelations(chainBundle())
	if !index.Holds("part", "holds") {
		t.Fatal("part does not hold Media through holds")
	}
	if index.Holds("maker", "made") {
		t.Fatal("a non-container kind holds Media")
	}
	if !index.Nests("part", "part-of", "volume") || index.Nests("part", "holds", "volume") {
		t.Fatal("part-of nesting not indexed exactly")
	}
}

func TestContainerNestingsListsEveryCrossKindNesting(t *testing.T) {
	got := ContainerNestings("kit", chainBundle())
	want := []store.ContainerNest{{PluginID: "kit", ChildKind: "part", Relation: "part-of", ParentKind: "volume"}}
	if !slices.Equal(got, want) {
		t.Fatalf("ContainerNestings = %+v, want %+v", got, want)
	}
	// One relation naming two parent kinds keeps both; a same-kind relation
	// is not a nesting.
	bundle := chainBundle()
	bundle.EntityKinds[0].Relations = append(bundle.EntityKinds[0].Relations,
		plugin.RelationSpec{Name: "part-of", ToKind: "shelf"},
		plugin.RelationSpec{Name: "sibling", ToKind: "part"},
	)
	got = ContainerNestings("kit", bundle)
	want = []store.ContainerNest{
		{PluginID: "kit", ChildKind: "part", Relation: "part-of", ParentKind: "volume"},
		{PluginID: "kit", ChildKind: "part", Relation: "part-of", ParentKind: "shelf"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ContainerNestings = %+v, want %+v", got, want)
	}
}

func TestOutermostContainerKindsAreChainEnds(t *testing.T) {
	got := slices.Sorted(maps.Keys(OutermostContainerKinds(chainBundle())))
	if !slices.Equal(got, []string{"volume"}) {
		t.Fatalf("outermost = %v, want [volume]", got)
	}
	single := plugin.Bundle{EntityKinds: []plugin.EntityKind{
		{Name: "part", Archetype: "container", Relations: []plugin.RelationSpec{
			{Name: "holds", ToKind: plugin.MediaKindName, Cardinality: "one"},
		}},
	}}
	if got := OutermostContainerKinds(single); !got["part"] || len(got) != 1 {
		t.Fatalf("single-level outermost = %v, want part", got)
	}
}

// A person credited in both cast and crew keeps their cast billing, not the
// crew ordinal that sorts after every cast member (CORE-2545).
func TestContainerRelationRowsKeepSmallestOrdinalForDuplicateCredit(t *testing.T) {
	ord := func(n int) *int { return &n }
	declared := map[string]plugin.RelationSpec{"credit": {Name: "credit", ToKind: "person"}}
	rows := containerRelationRows("lib", "entity-show", declared, []scrape.EntityLink{
		{Relation: "credit", Kind: "person", Name: "Lead Actor", SortOrder: ord(0)},
		{Relation: "credit", Kind: "person", Name: "Second Actor", SortOrder: ord(1)},
		{Relation: "credit", Kind: "person", Name: "Lead Actor", SortOrder: ord(1000)},
		{Relation: "credit", Kind: "person", Name: "Only Director", SortOrder: ord(1001)},
	})
	got := map[string]int{}
	for _, row := range rows {
		got[string(row.ToEntityID)] = *row.SortOrder
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (one per person): %v", len(rows), got)
	}
	if lead := got[string(entityIDForName("lib", "person", "Lead Actor"))]; lead != 0 {
		t.Fatalf("Lead Actor ordinal = %d, want the cast ordinal 0", lead)
	}
}
