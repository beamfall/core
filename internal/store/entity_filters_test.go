package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func registerGadgetTwoTextFilters(t *testing.T, st *Store) {
	t.Helper()
	err := st.RegisterEntityFieldIndexes(context.Background(), []EntityFieldIndex{
		{Kind: "gadget", Field: "color", Type: EntityFieldText, Filterable: true},
		{Kind: "gadget", Field: "material", Type: EntityFieldText, Filterable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func gadgetEntity(id EntityID, title, color, material string) Entity {
	return Entity{
		ID:        id,
		LibraryID: "library-local",
		Kind:      "gadget",
		Title:     title,
		Fields: []EntityField{
			{Name: "color", Type: EntityFieldText, Text: color},
			{Name: "material", Type: EntityFieldText, Text: material},
		},
	}
}

// TestEntityFilterJoinsGivesEachFilterADeterministicAlias covers the exact
// regression named in this ticket: internal/store/entity_index.go used to
// hard-code the single join alias "ff", which collides once a second filter
// on the SAME typed side table is added. entityFilterJoins must give filter i
// its own alias fN so two Text filters compose instead of colliding.
func TestEntityFilterJoinsGivesEachFilterADeterministicAlias(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	filters := []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "red"},
		{Name: "material", Type: EntityFieldText, Text: "steel"},
	}

	st.mu.RLock()
	joins, args, err := st.entityFilterJoins("gadget", filters)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joins, " f0 ") || !strings.Contains(joins, " f1 ") {
		t.Fatalf("joins = %q, want distinct f0/f1 aliases", joins)
	}
	if strings.Count(joins, "entity_field_text") != 2 {
		t.Fatalf("joins = %q, want two entity_field_text joins (same side table, distinct aliases)", joins)
	}
	if strings.Count(joins, ".kind = ?") != 2 {
		t.Fatalf("joins = %q, want one kind predicate per filter alias", joins)
	}
	wantArgs := []any{"gadget", "color", "red", "gadget", "material", "steel"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}

	// The legacy singular Filter folds into the same list, ahead of Filters.
	opts := EntityKindOptions{
		Filter:  &EntityField{Name: "color", Type: EntityFieldText, Text: "red"},
		Filters: []EntityField{{Name: "material", Type: EntityFieldText, Text: "steel"}},
	}
	combined := entityKindOptionsFilters(opts)
	if len(combined) != 2 || combined[0].Name != "color" || combined[1].Name != "material" {
		t.Fatalf("entityKindOptionsFilters = %#v, want [color, material]", combined)
	}
}

// TestEntitiesByKindCursorFilteredNarrowsAndStaysStable proves two Filters
// backed by the SAME typed side table (both Text) narrow correctly (rather
// than colliding or silently matching only the last-joined predicate) and
// that the filtered keyset walk stays stable across pages.
func TestEntitiesByKindCursorFilteredNarrowsAndStaysStable(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)

	seed := []Entity{
		gadgetEntity("gadget-alpha", "Alpha", "red", "steel"),     // matches both
		gadgetEntity("gadget-bravo", "Bravo", "red", "wood"),      // color only
		gadgetEntity("gadget-charlie", "Charlie", "red", "steel"), // matches both
		gadgetEntity("gadget-delta", "Delta", "blue", "steel"),    // material only
		gadgetEntity("gadget-echo", "Echo", "red", "steel"),       // matches both
	}
	for _, e := range seed {
		if err := st.UpsertEntity(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	opts := EntityKindOptions{Filters: []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "red"},
		{Name: "material", Type: EntityFieldText, Text: "steel"},
	}}
	filter := LibraryFilter{AllLibraries: true}

	var walked []string
	cursor := ""
	for page := 0; page < 5; page++ { // bounded: a stuck cursor must not hang the test
		got, err := st.EntitiesByKindCursorFiltered(ctx, "library-local", "gadget", filter, opts, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Entities) != 1 {
			t.Fatalf("page %d = %#v, want exactly one row (limit=1)", page, got.Entities)
		}
		walked = append(walked, string(got.Entities[0].ID))
		if got.Next == "" {
			break
		}
		cursor = got.Next
	}
	want := []string{"gadget-alpha", "gadget-charlie", "gadget-echo"}
	if len(walked) != len(want) {
		t.Fatalf("filtered cursor walk = %v, want %v (Bravo/Delta must never appear)", walked, want)
	}
	for i, id := range want {
		if walked[i] != id {
			t.Fatalf("filtered cursor walk = %v, want %v", walked, want)
		}
	}

	// A one-shot large-limit read confirms the same narrowing without paging.
	all, err := st.EntitiesByKindCursorFiltered(ctx, "library-local", "gadget", filter, opts, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Entities) != 3 || all.Next != "" {
		t.Fatalf("filtered single-page read = %#v, want the same 3 rows and no Next", all.Entities)
	}

	// EntitiesByKindCursor (the frozen signature) still delegates to the zero
	// EntityKindOptions and sees every gadget, unfiltered.
	unfiltered, err := st.EntitiesByKindCursor(ctx, "library-local", "gadget", filter, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(unfiltered.Entities) != len(seed) {
		t.Fatalf("EntitiesByKindCursor (unfiltered) = %d entities, want %d", len(unfiltered.Entities), len(seed))
	}
}

// TestEntitiesByKindCursorFilteredRejectsInvalidFilter proves an
// unregistered/non-Filterable/mistyped field errors instead of silently
// returning an unfiltered page.
func TestEntitiesByKindCursorFilteredRejectsInvalidFilter(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	if err := st.UpsertEntity(ctx, gadgetEntity("gadget-alpha", "Alpha", "red", "steel")); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}

	cases := []EntityKindOptions{
		{Filters: []EntityField{{Name: "never-registered", Type: EntityFieldText, Text: "x"}}},
		{Filters: []EntityField{{Name: "color", Type: EntityFieldNumber, Number: 1}}}, // type mismatch
	}
	for i, opts := range cases {
		if _, err := st.EntitiesByKindCursorFiltered(ctx, "library-local", "gadget", filter, opts, "", 50); err == nil {
			t.Fatalf("case %d: invalid filter accepted: %#v", i, opts)
		}
	}
}

func TestEntityFiltersRejectWrongKindSideRow(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	if err := st.RegisterEntityKindPolicies(ctx, []EntityKindPolicy{{
		Kind: "widget", FullTextSearchable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterEntityFieldIndexes(ctx, []EntityFieldIndex{{
		Kind: "widget", Field: "genre", Type: EntityFieldText, Filterable: true,
	}}); err != nil {
		t.Fatal(err)
	}
	entity := Entity{
		ID: "widget-wrong-kind-row", LibraryID: "library-local", Kind: "widget", Title: "Beacon",
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := st.catalogW.ExecContext(ctx, `
		INSERT INTO entity_field_text(entity_id, kind, field, field_type, value)
		VALUES (?, 'gadget', 'genre', 'text', 'scifi')
	`, entity.ID); err != nil {
		t.Fatal(err)
	}

	opts := EntityKindOptions{Filters: []EntityField{{
		Name: "genre", Type: EntityFieldText, Text: "scifi",
	}}}
	page, err := st.EntitiesByKindCursorFiltered(
		ctx, entity.LibraryID, entity.Kind, LibraryFilter{AllLibraries: true}, opts, "", 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entities) != 0 {
		t.Errorf("cursor filter matched wrong-kind side row: %v", page.Entities)
	}

	visible, restricted := visibleAndRestricted(t, st, LibraryFilter{AllLibraries: true})
	st.mu.RLock()
	ranked, err := st.fuzzySearchEntitiesLocked(ctx, visible, restricted, "beacon", 10, entity.Kind, opts.Filters)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 0 {
		t.Errorf("ranked filter matched wrong-kind side row: %v", ranked)
	}
}
