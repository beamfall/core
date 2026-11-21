package app

import (
	"testing"

	"github.com/beamfall/core/internal/plugin"
)

func TestNewRegistersPluginEntityKindPolicies(t *testing.T) {
	bundle := testBundle("chapters")
	bundle.EntityKinds = []plugin.EntityKind{
		{
			Name:                "chapter",
			Archetype:           "container",
			OrderedChildren:     true,
			FullTextSearchable:  true,
			OrphanPolicy:        "gc",
			CursorPaginated:     true,
			OrderingSourceField: "ordinal",
			Fields: []plugin.FieldSpec{{
				Name: "ordinal", Type: "number",
			}},
		},
		{Name: "topic", Archetype: "container"},
		{Name: "part", Archetype: "container", Relations: []plugin.RelationSpec{
			{Name: "holds", ToKind: plugin.MediaKindName, Cardinality: "one"},
			{Name: "part-of", ToKind: "volume"},
		}},
		{Name: "volume", Archetype: "container"},
		{Name: "speaker", Archetype: "person"},
	}
	beamfall, err := New(t.Context(), testConfig(t), nil, []plugin.Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = beamfall.Close() })

	policy, ok := beamfall.store.EntityKindPolicy("chapter")
	if !ok {
		t.Fatal("chapter policy was not registered")
	}
	if !policy.FullTextSearchable || !policy.OrphanGC || !policy.CursorPaginated || !policy.Container {
		t.Fatalf("chapter policy = %#v", policy)
	}
	// A container-archetype kind carries the Container posture even with no
	// other opt-in (CORE-2822); a kind of another archetype is not registered.
	if policy, ok := beamfall.store.EntityKindPolicy("topic"); !ok || !policy.Container || policy.FullTextSearchable || policy.OrphanGC || policy.CursorPaginated {
		t.Fatalf("topic policy = %#v, registered = %t", policy, ok)
	}
	if _, ok := beamfall.store.EntityKindPolicy("speaker"); ok {
		t.Fatal("non-container speaker policy was registered")
	}
	// A container chain's outermost kind is title-searchable by default
	// (CORE-2525); the inner kind of the chain is not.
	if policy, ok := beamfall.store.EntityKindPolicy("volume"); !ok || !policy.FullTextSearchable || !policy.Container || policy.OrphanGC {
		t.Fatalf("outermost container policy = %#v, registered = %t", policy, ok)
	}
	if policy, ok := beamfall.store.EntityKindPolicy("part"); !ok || policy.FullTextSearchable || !policy.Container {
		t.Fatalf("inner container policy = %#v, registered = %t", policy, ok)
	}
	ordering, ok := beamfall.store.EntityKindOrdering("chapter")
	if !ok || ordering.SourceField != "ordinal" {
		t.Fatalf("chapter ordering = %#v, registered = %t", ordering, ok)
	}
	if _, ok := beamfall.store.EntityKindOrdering("topic"); ok {
		t.Fatal("topic ordering was registered")
	}
}
