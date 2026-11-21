package store

import (
	"context"
	"strings"
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

// TestPlaceEntityRelationRenumbersAroundHiddenRows covers the Manage group
// re-order (CORE-2913): a placement counts only the rows the caller sees,
// renumbers the whole relation densely in one write, keeps the hidden rows'
// order and every existing row's attributes, and appends a new row past the
// last visible one.
func TestPlaceEntityRelationRenumbersAroundHiddenRows(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	other, err := st.CreateLibrary(ctx, "Other", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entity{
		{ID: "group", LibraryID: "library-local", Kind: "container", Title: "Group"},
		{ID: "hidden", LibraryID: other.ID, Kind: "container", Title: "Hidden"},
		{ID: "v1", LibraryID: "library-local", Kind: "container", Title: "V1"},
		{ID: "v2", LibraryID: "library-local", Kind: "container", Title: "V2"},
		{ID: "v3", LibraryID: "library-local", Kind: "container", Title: "V3"},
		{ID: "new", LibraryID: "library-local", Kind: "container", Title: "New"},
	} {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	zero, one := 0, 1
	for _, rel := range []EntityRelation{
		{FromEntityID: "group", Name: "contains", ToEntityID: "hidden", SortOrder: &zero},
		{FromEntityID: "group", Name: "contains", ToEntityID: "v1", SortOrder: &one, Attributes: map[string]string{"role": "lead"}},
		{FromEntityID: "group", Name: "contains", ToEntityID: "v2"},
		{FromEntityID: "group", Name: "contains", ToEntityID: "v3"},
	} {
		if err := st.UpsertEntityRelation(ctx, rel); err != nil {
			t.Fatal(err)
		}
	}
	local := LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}
	order := func() ([]string, map[string]EntityRelationRow) {
		t.Helper()
		rows, err := st.EntityRelationRows(ctx, "group", "contains", LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		ids, byID := []string{}, map[string]EntityRelationRow{}
		for i, row := range rows {
			if row.SortOrder == nil || (i > 0 && *row.SortOrder <= *rows[i-1].SortOrder) {
				t.Fatalf("row %d %s sortOrder = %v, want a number above the row before", i, row.ToEntity.ID, row.SortOrder)
			}
			ids = append(ids, string(row.ToEntity.ID))
			byID[string(row.ToEntity.ID)] = row
		}
		return ids, byID
	}

	if err := st.PlaceEntityRelation(ctx, EntityRelation{FromEntityID: "group", Name: "contains", ToEntityID: "v3"}, 0, local); err != nil {
		t.Fatal(err)
	}
	ids, byID := order()
	if got, want := strings.Join(ids, ","), "hidden,v3,v1,v2"; got != want {
		t.Fatalf("after move order = %s, want %s", got, want)
	}
	if byID["v1"].Attributes["role"] != "lead" {
		t.Fatalf("v1 attributes = %v, want role=lead kept", byID["v1"].Attributes)
	}

	// The visible rows reuse the numbers they held, and the hidden row is
	// not rewritten, so no stored number counts it.
	if got := *byID["hidden"].SortOrder; got != 0 {
		t.Fatalf("hidden sortOrder = %d, want 0 unchanged", got)
	}
	if got := *byID["v3"].SortOrder; got != 1 {
		t.Fatalf("v3 sortOrder = %d, want 1 (the number v1 held)", got)
	}
	// A target the filter cannot see is refused and nothing moves.
	if err := st.PlaceEntityRelation(ctx, EntityRelation{FromEntityID: "group", Name: "contains", ToEntityID: "hidden"}, 2, local); err == nil {
		t.Fatal("hidden target placed")
	}
	if ids, _ := order(); strings.Join(ids, ",") != "hidden,v3,v1,v2" {
		t.Fatalf("after refused move order = %v", ids)
	}

	if err := st.PlaceEntityRelation(ctx, EntityRelation{FromEntityID: "group", Name: "contains", ToEntityID: "new"}, 99, local); err != nil {
		t.Fatal(err)
	}
	if ids, _ := order(); strings.Join(ids, ",") != "hidden,v3,v1,v2,new" {
		t.Fatalf("after append order = %v", ids)
	}

	if err := st.PlaceEntityRelation(ctx, EntityRelation{FromEntityID: "group", Name: "contains", ToEntityID: "v1"}, -1, local); err == nil {
		t.Fatal("negative position accepted")
	}
}
