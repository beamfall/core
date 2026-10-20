package store

import (
	"context"
	"strings"
	"testing"
)

func seedFilterDispatchGadget(t *testing.T, st *Store, library LibraryID, id EntityID, title, color, material string) {
	t.Helper()
	entity := Entity{
		ID:        id,
		LibraryID: library,
		Kind:      "gadget",
		Title:     title,
		Fields: []EntityField{
			{Name: "color", Type: EntityFieldText, Text: color},
			{Name: "material", Type: EntityFieldText, Text: material},
		},
	}
	if err := st.UpsertEntity(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
}

func gadgetDispatchFilters() []EntityField {
	return []EntityField{
		{Name: "color", Type: EntityFieldText, Text: "red"},
		{Name: "material", Type: EntityFieldText, Text: "steel"},
	}
}

func TestEntityItemsDispatchFiltersBeforeLimit(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	seedFilterDispatchGadget(t, st, "library-local", "gadget-alpha", "Alpha", "red", "wood")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-bravo", "Bravo", "red", "steel")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-charlie", "Charlie", "red", "steel")

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind:               "entity-items",
		EntityKind:         "gadget",
		EntityFieldFilters: gadgetDispatchFilters(),
		Limit:              1,
	})
	if len(items) != 1 || items[0].ID != "gadget-bravo" {
		t.Fatalf("filtered entity-items = %#v, want Bravo; filters must apply before LIMIT", items)
	}
}

func TestEntityItemsDispatchMatchesCursorFiltered(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	seedFilterDispatchGadget(t, st, "library-local", "gadget-alpha", "Alpha", "red", "steel")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-bravo", "Bravo", "blue", "steel")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-charlie", "Charlie", "red", "steel")
	filters := gadgetDispatchFilters()

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind:               "entity-items",
		EntityKind:         "gadget",
		EntityFieldFilters: filters,
		LibraryID:          "library-local",
	})
	page, err := st.EntitiesByKindCursorFiltered(
		ctx,
		"library-local",
		"gadget",
		LibraryFilter{AllLibraries: true},
		EntityKindOptions{Filters: filters},
		"",
		200,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]MediaID, len(page.Entities))
	for i, entity := range page.Entities {
		want[i] = MediaID(entity.ID)
	}
	if got := sourceItemIDs(items); !equalMediaIDs(got, want) {
		t.Fatalf("filtered entity-items IDs = %v, cursor-filtered IDs = %v", got, want)
	}
}

func TestEntityItemsDispatchRejectsInvalidFilter(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)

	_, err := st.SourceItems(context.Background(), LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind:       "entity-items",
		EntityKind: "gadget",
		EntityFieldFilters: []EntityField{
			{Name: "never-registered", Type: EntityFieldText, Text: "x"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "field never-registered is not indexed on kind gadget") {
		t.Fatalf("invalid filter error = %v, want registered-field error", err)
	}
}

func TestEntityItemsDispatchDoesNotExposeFilterSchemaWithoutVisibleLibraries(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)

	items, err := st.SourceItems(context.Background(), LibraryFilter{}, SourceQuery{
		Kind:       "entity-items",
		EntityKind: "gadget",
		EntityFieldFilters: []EntityField{
			{Name: "never-registered", Type: EntityFieldText, Text: "x"},
		},
	})
	if err != nil {
		t.Fatalf("zero-grant filtered entity-items error = %v, want hidden and missing indistinguishable", err)
	}
	if len(items) != 0 {
		t.Fatalf("zero-grant filtered entity-items = %#v, want empty", items)
	}
}

func TestEntityItemsDispatchWithoutFiltersKeepsExistingBehavior(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	seedFilterDispatchGadget(t, st, "library-local", "gadget-alpha", "Alpha", "red", "wood")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-bravo", "Bravo", "blue", "steel")

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind:               "entity-items",
		EntityKind:         "gadget",
		EntityFieldFilters: []EntityField{},
	})
	want := []MediaID{"gadget-alpha", "gadget-bravo"}
	if got := sourceItemIDs(items); !equalMediaIDs(got, want) {
		t.Fatalf("unfiltered entity-items IDs = %v, want %v", got, want)
	}
}

func TestEntityItemsDispatchFiltersAcrossVisibleLibraries(t *testing.T) {
	st := openSeeded(t)
	registerGadgetTwoTextFilters(t, st)
	insertPluginLibrary(t, st, "library-second", "demo")
	seedFilterDispatchGadget(t, st, "library-local", "gadget-local", "Local", "red", "steel")
	seedFilterDispatchGadget(t, st, "library-second", "gadget-second", "Second", "red", "steel")
	seedFilterDispatchGadget(t, st, "library-second", "gadget-other", "Unmatched", "blue", "steel")

	items := mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{
		Kind:               "entity-items",
		EntityKind:         "gadget",
		EntityFieldFilters: gadgetDispatchFilters(),
	})
	want := []MediaID{"gadget-local", "gadget-second"}
	if got := sourceItemIDs(items); !equalMediaIDs(got, want) {
		t.Fatalf("multi-library filtered entity-items IDs = %v, want %v", got, want)
	}
}
