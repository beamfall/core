package identify

import (
	"maps"
	"slices"
	"testing"

	"github.com/beamfall/core/internal/plugin"
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
