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
	if !policy.FullTextSearchable || !policy.OrphanGC || !policy.CursorPaginated {
		t.Fatalf("chapter policy = %#v", policy)
	}
	if _, ok := beamfall.store.EntityKindPolicy("topic"); ok {
		t.Fatal("non-opted-in topic policy was registered")
	}
	ordering, ok := beamfall.store.EntityKindOrdering("chapter")
	if !ok || ordering.SourceField != "ordinal" {
		t.Fatalf("chapter ordering = %#v, registered = %t", ordering, ok)
	}
	if _, ok := beamfall.store.EntityKindOrdering("topic"); ok {
		t.Fatal("topic ordering was registered")
	}
}
