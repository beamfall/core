package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// relatedFixture seeds a shelf Library and a restricted one with Entities of
// three kinds and returns the open store.
func relatedFixture(t *testing.T) (*Store, Library, Library) {
	t.Helper()
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := st.CreateLibrary(ctx, "Locked", true, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	return st, lib, hidden
}

func upsertRelatedEntity(t *testing.T, st *Store, id EntityID, library LibraryID, kind, title string) {
	t.Helper()
	if err := st.UpsertEntity(context.Background(), Entity{ID: id, LibraryID: library, Kind: kind, Title: title}); err != nil {
		t.Fatal(err)
	}
}

func relateEntities(t *testing.T, st *Store, relations ...EntityRelation) {
	t.Helper()
	for _, relation := range relations {
		if err := st.UpsertEntityRelation(context.Background(), relation); err != nil {
			t.Fatal(err)
		}
	}
}

// groupIDs flattens groups to kind → ordered Entity ids for comparison.
func groupIDs(groups []RelatedEntityGroup) map[string][]MediaID {
	out := map[string][]MediaID{}
	for _, group := range groups {
		for _, item := range group.Items {
			out[group.Kind] = append(out[group.Kind], item.ID)
		}
	}
	return out
}

// TestRelatedEntityGroupsBothDirections pins that an Entity lists the
// Entities it points at and the Entities pointing at it, grouped by kind in
// kind-name order, each Entity once, never itself, and never a Media row.
func TestRelatedEntityGroupsBothDirections(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	upsertRelatedEntity(t, st, "entity-maker", lib.ID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-box-a", lib.ID, "box", "Alpha")
	upsertRelatedEntity(t, st, "entity-box-b", lib.ID, "box", "Beta")
	upsertRelatedEntity(t, st, "entity-place", lib.ID, "place", "Place")
	upsertRelatedEntity(t, st, "entity-lone", lib.ID, "maker", "Lone")
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/item.flac", Title: "Item", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "fp-item",
	})
	if err != nil {
		t.Fatal(err)
	}
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-box-a", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-b", Name: "made-by", ToEntityID: "entity-maker"},
		// A second relation to the same Entity must not duplicate it.
		EntityRelation{FromEntityID: "entity-box-b", Name: "sold-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-maker", Name: "based-in", ToEntityID: "entity-place"},
		EntityRelation{FromEntityID: "entity-maker", Name: "made", ToMediaID: media.ID},
	)

	groups, err := st.RelatedEntityGroups(ctx, "entity-maker", server)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{
		"box":   {"entity-box-a", "entity-box-b"},
		"place": {"entity-place"},
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups(maker) = %v, want %v", got, want)
	}
	if len(groups) != 2 || groups[0].Kind != "box" || groups[1].Kind != "place" {
		t.Fatalf("groups not in kind order: %+v", groups)
	}
	if item := groups[0].Items[0]; item.Kind != "entity" || item.Title != "Alpha" {
		t.Fatalf("related item = %+v, want an entity item titled Alpha", item)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-box-a", server)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"maker": {"entity-maker"}}) {
		t.Fatalf("RelatedEntityGroups(box) = %v, want the maker it points at", got)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-lone", server)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Fatalf("RelatedEntityGroups(lone) = %+v, want none", groups)
	}
}

// TestRelatedEntityGroupsHonorVisibility pins the Reveal funnel: trashed
// related Entities and Entities in Libraries the filter hides are omitted.
func TestRelatedEntityGroupsHonorVisibility(t *testing.T) {
	st, lib, hidden := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	upsertRelatedEntity(t, st, "entity-maker", lib.ID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-box-open", lib.ID, "box", "Open")
	upsertRelatedEntity(t, st, "entity-box-gone", lib.ID, "box", "Gone")
	upsertRelatedEntity(t, st, "entity-box-locked", hidden.ID, "box", "Locked")
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-box-open", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-gone", Name: "made-by", ToEntityID: "entity-maker"},
		EntityRelation{FromEntityID: "entity-box-locked", Name: "made-by", ToEntityID: "entity-maker"},
	)
	if err := st.TrashEntity(ctx, "entity-box-gone", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	unrevealed := LibraryFilter{AllLibraries: true}
	groups, err := st.RelatedEntityGroups(ctx, "entity-maker", unrevealed)
	if err != nil {
		t.Fatal(err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"box": {"entity-box-open"}}) {
		t.Fatalf("unrevealed RelatedEntityGroups = %v, want only the open box", got)
	}

	groups, err = st.RelatedEntityGroups(ctx, "entity-maker", server)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{"box": {"entity-box-locked", "entity-box-open"}}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("revealed RelatedEntityGroups = %v, want %v", got, want)
	}
	for _, item := range groups[0].Items {
		if item.Restricted != (item.ID == "entity-box-locked") {
			t.Fatalf("item %s restricted = %v", item.ID, item.Restricted)
		}
	}
}

// TestRelatedEntityGroupsHiddenContextEntity pins that the context Entity
// must itself be visible: a hidden one errors like a missing one.
func TestRelatedEntityGroupsHiddenContextEntity(t *testing.T) {
	st, lib, hidden := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-locked", hidden.ID, "maker", "Locked")
	upsertRelatedEntity(t, st, "entity-box", lib.ID, "box", "Box")
	relateEntities(t, st, EntityRelation{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-locked"})

	if _, err := st.RelatedEntityGroups(ctx, "entity-locked", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("hidden context Entity returned related groups")
	}
	if _, err := st.RelatedEntityGroups(ctx, "entity-missing", LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("missing context Entity returned related groups")
	}
}

// TestRelatedEntityGroupsOrderBySortOrderThenTitle pins in-group ordering:
// relation rows with a sort order first in that order, then by title.
func TestRelatedEntityGroupsOrderBySortOrderThenTitle(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	upsertRelatedEntity(t, st, "entity-shelf", lib.ID, "rack", "Rack")
	upsertRelatedEntity(t, st, "entity-a", lib.ID, "box", "Aardvark")
	upsertRelatedEntity(t, st, "entity-b", lib.ID, "box", "Zebra")
	upsertRelatedEntity(t, st, "entity-c", lib.ID, "box", "Mango")
	upsertRelatedEntity(t, st, "entity-d", lib.ID, "box", "Banana")
	one, two := 1, 2
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-a"},
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-b", SortOrder: &two},
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-c", SortOrder: &one},
		EntityRelation{FromEntityID: "entity-d", Name: "sits-on", ToEntityID: "entity-shelf"},
	)

	groups, err := st.RelatedEntityGroups(ctx, "entity-shelf", LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MediaID{"box": {"entity-c", "entity-b", "entity-a", "entity-d"}}
	if got := groupIDs(groups); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups order = %v, want %v", got, want)
	}
}
