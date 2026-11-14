package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestEntityArtworkSourceReads pins the three reads the artwork sweep's
// Entity pass walks with: Entities of one plugin's kind by id cursor, an
// Entity's member Media in the order it shows them, and the Entities of a
// kind pointing at an Entity by title. Trashed rows and rows from another
// Library are never offered.
func TestEntityArtworkSourceReads(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	server := LibraryFilter{Revealed: true, AllLibraries: true}
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "Other", false, "other", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	entity := func(id EntityID, library LibraryID, kind, title string) {
		t.Helper()
		if err := st.UpsertEntity(ctx, Entity{ID: id, LibraryID: library, Kind: kind, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	memberIn := func(library LibraryID, name string) MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: library, Path: "/" + string(library) + "/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	member := func(name string) MediaID { return memberIn(lib.ID, name) }
	entity("entity-box-a", lib.ID, "box", "Zeta")
	entity("entity-box-b", lib.ID, "box", "Alpha")
	entity("entity-box-c", lib.ID, "box", "Gone")
	entity("entity-maker", lib.ID, "maker", "Maker")
	entity("entity-foreign", other.ID, "box", "Foreign")
	if err := st.TrashEntity(ctx, "entity-box-c", server, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	refs, err := st.EntitiesOfKindAfter(ctx, "shelf", "box", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []EntityRef{{ID: "entity-box-a", LibraryID: lib.ID}, {ID: "entity-box-b", LibraryID: lib.ID}}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("EntitiesOfKindAfter = %v, want %v", refs, want)
	}
	refs, err = st.EntitiesOfKindAfter(ctx, "shelf", "box", "entity-box-a", 10)
	if err != nil || len(refs) != 1 || refs[0].ID != "entity-box-b" {
		t.Fatalf("EntitiesOfKindAfter(cursor) = %v, %v; want entity-box-b", refs, err)
	}
	if refs, err = st.EntitiesOfKindAfter(ctx, "shelf", "box", "", 1); err != nil || len(refs) != 1 {
		t.Fatalf("EntitiesOfKindAfter(limit 1) = %v, %v", refs, err)
	}

	late, early, unordered, trashed := member("late"), member("early"), member("unordered"), member("trashed")
	foreign := memberIn(other.ID, "foreign")
	two, one, three, zero := 2, 1, 3, 0
	for _, relation := range []EntityRelation{
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: unordered},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: late, SortOrder: &two},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: early, SortOrder: &one},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: trashed, SortOrder: &three},
		{FromEntityID: "entity-box-a", Name: "lists", ToMediaID: early},
		{FromEntityID: "entity-box-a", Name: "made-by", ToEntityID: "entity-maker"},
		{FromEntityID: "entity-box-b", Name: "made-by", ToEntityID: "entity-maker"},
		{FromEntityID: "entity-box-c", Name: "made-by", ToEntityID: "entity-maker"},
		{FromEntityID: "entity-box-b", Name: "sold-by", ToEntityID: "entity-maker"},
		{FromEntityID: "entity-box-a", Name: "holds", ToMediaID: foreign, SortOrder: &zero},
		{FromEntityID: "entity-foreign", Name: "made-by", ToEntityID: "entity-maker"},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.TrashMedia(ctx, trashed, server); err != nil {
		t.Fatal(err)
	}

	members, err := st.EntityMemberMediaIDs(ctx, "entity-box-a", "holds", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []MediaID{early, late, unordered}; !reflect.DeepEqual(members, want) {
		t.Fatalf("EntityMemberMediaIDs = %v, want %v", members, want)
	}
	if page, err := st.EntityMemberMediaIDs(ctx, "entity-box-a", "holds", 1, 1); err != nil || !reflect.DeepEqual(page, []MediaID{late}) {
		t.Fatalf("EntityMemberMediaIDs page 2 = %v, %v; want [%s]", page, err, late)
	}

	makers, err := st.EntitiesRelatingTo(ctx, "entity-maker", "box", "made-by", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []EntityID{"entity-box-b", "entity-box-a"}; !reflect.DeepEqual(makers, want) {
		t.Fatalf("EntitiesRelatingTo = %v, want %v (by title)", makers, want)
	}
	if page, err := st.EntitiesRelatingTo(ctx, "entity-maker", "box", "made-by", 1, 1); err != nil || !reflect.DeepEqual(page, []EntityID{"entity-box-a"}) {
		t.Fatalf("EntitiesRelatingTo page 2 = %v, %v; want [entity-box-a]", page, err)
	}
	if none, err := st.EntitiesRelatingTo(ctx, "entity-maker", "maker", "made-by", 0, 10); err != nil || len(none) != 0 {
		t.Fatalf("EntitiesRelatingTo(other kind) = %v, %v; want none", none, err)
	}
}
