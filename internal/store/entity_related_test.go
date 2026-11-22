package store

import (
	"context"
	"reflect"
	"strconv"
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
	if groups[0].Direction != RelatedEntityChild || groups[1].Direction != RelatedEntityParent {
		t.Fatalf("group directions = %q, %q; want the boxes pointing at maker as children and the place it points at as a parent",
			groups[0].Direction, groups[1].Direction)
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

// TestRelatedEntityGroupsDirectionThreeLevels pins relation direction over a
// three-level container graph (CORE-2036): a middle container lists the
// container below it as a child and the one above it as a parent, even when
// the parent's kind sorts first, and one kind related both ways splits into a
// child group and a parent group, with an Entity linked both ways listed once
// as a parent.
func TestRelatedEntityGroupsDirectionThreeLevels(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	// "a-top" sorts before "b-middle" and "c-bottom", so a kind-only read would
	// put the parent group first.
	upsertRelatedEntity(t, st, "entity-top", lib.ID, "a-top", "Top")
	upsertRelatedEntity(t, st, "entity-middle", lib.ID, "b-middle", "Middle")
	upsertRelatedEntity(t, st, "entity-bottom-1", lib.ID, "c-bottom", "Bottom 1")
	upsertRelatedEntity(t, st, "entity-bottom-2", lib.ID, "c-bottom", "Bottom 2")
	upsertRelatedEntity(t, st, "entity-peer-up", lib.ID, "b-middle", "Peer Up")
	upsertRelatedEntity(t, st, "entity-peer-down", lib.ID, "b-middle", "Peer Down")
	upsertRelatedEntity(t, st, "entity-peer-both", lib.ID, "b-middle", "Peer Both")
	relateEntities(t, st,
		EntityRelation{FromEntityID: "entity-middle", Name: "part-of", ToEntityID: "entity-top"},
		EntityRelation{FromEntityID: "entity-bottom-1", Name: "part-of", ToEntityID: "entity-middle"},
		EntityRelation{FromEntityID: "entity-bottom-2", Name: "part-of", ToEntityID: "entity-middle"},
		EntityRelation{FromEntityID: "entity-middle", Name: "follows", ToEntityID: "entity-peer-up"},
		EntityRelation{FromEntityID: "entity-peer-down", Name: "follows", ToEntityID: "entity-middle"},
		EntityRelation{FromEntityID: "entity-middle", Name: "follows", ToEntityID: "entity-peer-both"},
		EntityRelation{FromEntityID: "entity-peer-both", Name: "follows", ToEntityID: "entity-middle"},
	)

	type group struct {
		kind, direction string
		ids             []MediaID
	}
	read := func(id EntityID) []group {
		t.Helper()
		groups, err := st.RelatedEntityGroups(ctx, id, server)
		if err != nil {
			t.Fatal(err)
		}
		out := []group{}
		for _, g := range groups {
			ids := []MediaID{}
			for _, item := range g.Items {
				ids = append(ids, item.ID)
			}
			out = append(out, group{g.Kind, g.Direction, ids})
		}
		return out
	}

	want := []group{
		{"a-top", RelatedEntityParent, []MediaID{"entity-top"}},
		{"b-middle", RelatedEntityChild, []MediaID{"entity-peer-down"}},
		{"b-middle", RelatedEntityParent, []MediaID{"entity-peer-both", "entity-peer-up"}},
		{"c-bottom", RelatedEntityChild, []MediaID{"entity-bottom-1", "entity-bottom-2"}},
	}
	if got := read("entity-middle"); !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups(middle) = %+v, want %+v", got, want)
	}
	if got, want := read("entity-top"), []group{{"b-middle", RelatedEntityChild, []MediaID{"entity-middle"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups(top) = %+v, want %+v", got, want)
	}
	if got, want := read("entity-bottom-1"), []group{{"b-middle", RelatedEntityParent, []MediaID{"entity-middle"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RelatedEntityGroups(bottom) = %+v, want %+v", got, want)
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
		EntityRelation{FromEntityID: "entity-shelf", Name: "holds", ToEntityID: "entity-d"},
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

// TestRelatedEntityGroupsReadsTheEncryptedLibraryPlane pins that an encrypted
// Library's related Entities are read from that Library's own database, where
// its relations live, and that a sealed Library fails the read.
func TestRelatedEntityGroupsReadsTheEncryptedLibraryPlane(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "related-plane-target")
	defer st.Close()
	upsertRelatedEntity(t, st, "entity-plane-maker", media.LibraryID, "maker", "Maker")
	upsertRelatedEntity(t, st, "entity-plane-box", media.LibraryID, "box", "Box")
	relateEntities(t, st, EntityRelation{FromEntityID: "entity-plane-box", Name: "made-by", ToEntityID: "entity-plane-maker"})

	server := LibraryFilter{Revealed: true, AllLibraries: true}
	groups, err := st.RelatedEntityGroups(ctx, "entity-plane-maker", server)
	if err != nil {
		t.Fatalf("RelatedEntityGroups unsealed: %v", err)
	}
	if got := groupIDs(groups); !reflect.DeepEqual(got, map[string][]MediaID{"box": {"entity-plane-box"}}) {
		t.Fatalf("encrypted RelatedEntityGroups = %v, want the box from the Library plane", got)
	}

	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatalf("SealEncryptedLibraryDB: %v", err)
	}
	if _, err := st.RelatedEntityGroups(ctx, "entity-plane-maker", server); err == nil {
		t.Fatal("sealed encrypted RelatedEntityGroups succeeded")
	}
}

func TestRelatedEntityGroupsDeriveValuesFromCurrentVisibleMedia(t *testing.T) {
	st, lib, hidden := relatedFixture(t)
	ctx := t.Context()
	upsertRelatedEntity(t, st, "maker", lib.ID, "maker", "Maker")
	for _, id := range []EntityID{"box-a", "box-b"} {
		// A legacy stored Entity value must have no effect on grouping.
		if err := st.UpsertEntity(ctx, Entity{ID: id, LibraryID: lib.ID, Kind: "box", Title: string(id),
			Fields: []EntityField{{Name: "shape", Type: EntityFieldText, Text: "stale"}},
		}); err != nil {
			t.Fatal(err)
		}
		relateEntities(t, st, EntityRelation{FromEntityID: id, Name: "made-by", ToEntityID: "maker"})
	}
	setTag := func(id MediaID, value string) {
		t.Helper()
		if err := st.UpsertMediaFields(ctx, id, []MediaField{{Name: "shape", Type: EntityFieldText, Text: value}}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(id string, library LibraryID, tag string) MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: library, Path: "/shelf/" + id + ".flac", Title: id, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: id, Year: 2005,
		})
		if err != nil {
			t.Fatal(err)
		}
		setTag(media.ID, tag)
		relateEntities(t, st, EntityRelation{FromEntityID: "box-a", Name: "contains", ToMediaID: media.ID})
		return media.ID
	}
	add("hidden", hidden.ID, "alphabetically-first")
	add("dated", lib.ID, "long")
	moved := add("moved", lib.ID, "short")
	alsoShort := add("also-short", lib.ID, "short")
	relateEntities(t, st, EntityRelation{FromEntityID: "box-a", Name: "also-contains", ToMediaID: alsoShort})

	check := func(a, b string) {
		t.Helper()
		groups, err := st.RelatedEntityGroupsBy(ctx, "maker", LibraryFilter{AllLibraries: true}, func(owner, kind string) string {
			if owner == "shelf" && kind == "box" {
				return "shape"
			}
			return ""
		})
		if err != nil || len(groups) != 1 || len(groups[0].Items) != 2 {
			t.Fatalf("groups = %+v, err = %v", groups, err)
		}
		g := groups[0]
		if g.GroupValues["box-a"] != a || g.GroupValues["box-b"] != b || g.Items[0].Year != 2005 {
			t.Fatalf("values = %v, first year = %d, want %q, %q and 2005", g.GroupValues, g.Items[0].Year, a, b)
		}
	}
	check("short", "")
	if err := st.ReplaceMediaRelations(ctx, moved, []EntityRelation{{
		FromEntityID: "box-b", Name: "contains", ToMediaID: moved,
	}}); err != nil {
		t.Fatal(err)
	}
	check("long", "short") // tie on box A, even with a duplicate link to the short item
	setTag(moved, "")
	check("long", "") // the moved track was box B's only vote
}

func TestRelatedEntityYearsExceedSQLiteVariableLimit(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := t.Context()
	upsertRelatedEntity(t, st, "dated-entity", lib.ID, "box", "Dated")
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/dated.flac", Title: "Dated", Container: "flac", Codec: "flac",
		Class: MediaClassAudio, ContentFingerprint: "dated", Year: 2005,
	})
	if err != nil {
		t.Fatal(err)
	}
	relateEntities(t, st, EntityRelation{FromEntityID: "dated-entity", Name: "contains", ToMediaID: media.ID})

	// Exercise the same year-annotation path as RelatedEntityGroups without
	// inserting thousands of unrelated fixture rows.
	items := make([]SourceItem, 32_767)
	for i := range items {
		items[i] = SourceItem{ID: MediaID(strconv.Itoa(i)), Kind: "entity"}
	}
	items[len(items)-1].ID = "dated-entity"
	st.mu.RLock()
	defer st.mu.RUnlock()
	if err := st.annotateEntityYearsLocked(ctx, LibraryFilter{AllLibraries: true}, items); err != nil {
		t.Fatal(err)
	}
	if got := items[len(items)-1].Year; got != 2005 || items[0].Year != 0 {
		t.Fatalf("years = %d, %d; want 0, 2005", items[0].Year, got)
	}
}

func TestHydrationTagCandidatesReuseOnlyPerMediaLegacyValues(t *testing.T) {
	st, lib, _ := relatedFixture(t)
	ctx := t.Context()
	entity := Entity{ID: "box", LibraryID: lib.ID, Kind: "box", Title: "Box",
		Fields: []EntityField{{Name: "shape", Type: EntityFieldText, Text: "shared-and-stale"}},
	}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	results := map[MediaID]string{}
	for _, name := range []string{"with-legacy", "without-legacy"} {
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: name,
		})
		if err != nil {
			t.Fatal(err)
		}
		relateEntities(t, st, EntityRelation{FromEntityID: entity.ID, Name: "contains", ToMediaID: media.ID})
		results[media.ID] = ""
		if name == "with-legacy" {
			legacy := `{"attributes":{"label":"legacy label"},"@hydration":{"Field":"shape","Value":"oval"}}`
			if _, err := st.catalogW.ExecContext(ctx, "UPDATE entity_relation SET attributes = ? WHERE to_media_id = ?", legacy, media.ID); err != nil {
				t.Fatal(err)
			}
			results[media.ID] = "oval"
			links, err := st.EntitiesForMedia(ctx, media.ID, "contains", LibraryFilter{AllLibraries: true})
			if err != nil || len(links) != 1 || !reflect.DeepEqual(links[0].Attributes, map[string]string{"label": "legacy label"}) {
				t.Fatalf("legacy render attributes: %v, err = %v", links, err)
			}
		}
	}
	candidates, err := st.HydrationTagCandidates(ctx, lib.ID, []MediaClass{MediaClassAudio}, "shape")
	if err != nil {
		t.Fatal(err)
	}
	got := map[MediaID]string{}
	for _, candidate := range candidates {
		got[candidate.MediaID] = candidate.PersistedValue
	}
	if !reflect.DeepEqual(got, results) {
		t.Fatalf("per-Media candidates = %v, want %v", got, results)
	}
}
