package store

import (
	"context"
	"testing"
)

func TestRelatedEntitiesUsesDeclaredOrdinalWithoutRelationSortOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		registerField bool
		registerOrder bool
		relationOrder map[EntityID]int
		want          []EntityID
	}{
		{name: "declared ordering", registerField: true, registerOrder: true, want: []EntityID{"entity-zulu", "entity-alpha"}},
		{name: "default title ordering", registerField: true, want: []EntityID{"entity-alpha", "entity-zulu"}},
		{name: "unregistered field", registerOrder: true, want: []EntityID{"entity-alpha", "entity-zulu"}},
		{
			name:          "relation sort order takes precedence",
			registerField: true,
			registerOrder: true,
			relationOrder: map[EntityID]int{"entity-alpha": 2},
			want:          []EntityID{"entity-alpha", "entity-zulu"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSeeded(t)
			ctx := context.Background()
			if tc.registerField {
				if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{{
					Kind: "chapter", Field: "ordinal", Type: EntityFieldNumber,
				}}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.registerOrder {
				if err := st.RegisterEntityKindOrderings(ctx, []EntityKindOrdering{{
					Kind: "chapter", SourceField: "ordinal",
				}}); err != nil {
					t.Fatal(err)
				}
			}
			entities := []Entity{
				{ID: "entity-parent", LibraryID: "library-local", Kind: "chapter", Title: "Parent"},
				{ID: "entity-alpha", LibraryID: "library-local", Kind: "chapter", Title: "Alpha", Fields: []EntityField{{Name: "ordinal", Type: EntityFieldNumber, Number: 2}}},
				{ID: "entity-zulu", LibraryID: "library-local", Kind: "chapter", Title: "Zulu", Fields: []EntityField{{Name: "ordinal", Type: EntityFieldNumber, Number: 1}}},
			}
			for _, entity := range entities {
				if err := st.UpsertEntity(ctx, entity); err != nil {
					t.Fatal(err)
				}
			}
			for _, child := range []EntityID{"entity-alpha", "entity-zulu"} {
				var sortOrder *int
				if order, ok := tc.relationOrder[child]; ok {
					sortOrder = &order
				}
				if err := st.UpsertEntityRelation(ctx, EntityRelation{
					FromEntityID: "entity-parent",
					Name:         "child",
					ToEntityID:   child,
					SortOrder:    sortOrder,
				}); err != nil {
					t.Fatal(err)
				}
			}

			got, err := st.RelatedEntities(ctx, "entity-parent", "child", LibraryFilter{AllLibraries: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("RelatedEntities() len = %d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i].Entity.ID != tc.want[i] {
					t.Fatalf("RelatedEntities()[%d] = %q, want %q", i, got[i].Entity.ID, tc.want[i])
				}
			}
		})
	}
}
