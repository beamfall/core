package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestEntityTitleFoldAppliesOnlyToSharedCatalogLibraries pins CORE-2459: the
// title-fold merge journals in the shared catalog, so it stays off for an
// encrypted Library.
func TestEntityTitleFoldAppliesOnlyToSharedCatalogLibraries(t *testing.T) {
	st, media := seededEncryptedMedia(t, "fold-secret")
	defer st.Close()
	ctx := context.Background()
	if applies, err := st.EntityTitleFoldApplies(ctx, media.LibraryID); err != nil || applies {
		t.Fatalf("encrypted Library applies = %v, %v; want false", applies, err)
	}
	plain, err := st.CreateLibrary(ctx, "Plain", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if applies, err := st.EntityTitleFoldApplies(ctx, plain.ID); err != nil || !applies {
		t.Fatalf("shared-catalog Library applies = %v, %v; want true", applies, err)
	}
}

// TestLiveFoldEqualEntitiesKeepsItsIndexCurrent pins CORE-2459's title-fold
// lookup: it lists live variants oldest first, and its per-Library index sees
// what changed after it was built — a variant UpsertEntity wrote, a row
// written past the Store, a rename, a trash and a restore — while a scoped
// lookup lists only that scope's members.
func TestLiveFoldEqualEntitiesKeepsItsIndexCurrent(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	upsert := func(id EntityID, title string) {
		t.Helper()
		if err := st.UpsertEntity(ctx, Entity{ID: id, LibraryID: lib.ID, Kind: "artist", Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	lookup := func(title string) []EntityID {
		t.Helper()
		found, err := st.LiveFoldEqualEntities(ctx, lib.ID, "artist", title, "", "")
		if err != nil {
			t.Fatal(err)
		}
		var ids []EntityID
		for _, entity := range found {
			ids = append(ids, entity.ID)
		}
		return ids
	}
	expect := func(title string, want ...EntityID) {
		t.Helper()
		if got := lookup(title); !reflect.DeepEqual(got, want) {
			t.Fatalf("variants of %q = %v, want %v", title, got, want)
		}
	}
	upsert("fold-a", "First Band")
	upsert("fold-b", "first.band")
	upsert("fold-other", "Other Band")
	expect("FIRST BAND", "fold-a", "fold-b")

	upsert("fold-c", "FIRST-BAND")
	expect("first band", "fold-a", "fold-b", "fold-c")

	if _, err := st.catalogW.ExecContext(ctx, `INSERT INTO entity(id, library_id, kind, title) VALUES (?, ?, 'artist', ?)`, "fold-d", lib.ID, "first_band"); err != nil {
		t.Fatal(err)
	}
	expect("First Band", "fold-a", "fold-b", "fold-c", "fold-d")

	upsert("fold-b", "Second Band")
	expect("First Band", "fold-a", "fold-c", "fold-d")
	expect("second band", "fold-b")

	all := LibraryFilter{AllLibraries: true, Revealed: true}
	if err := st.TrashEntity(ctx, "fold-a", all, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	expect("First Band", "fold-c", "fold-d")
	if err := st.RestoreEntity(ctx, "fold-a", all); err != nil {
		t.Fatal(err)
	}
	expect("First Band", "fold-a", "fold-c", "fold-d")

	upsert("fold-scope", "Scope")
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "fold-c", Name: "member-of", ToEntityID: "fold-scope"}); err != nil {
		t.Fatal(err)
	}
	scoped, err := st.LiveFoldEqualEntities(ctx, lib.ID, "artist", "first band", "member-of", "fold-scope")
	if err != nil || len(scoped) != 1 || scoped[0].ID != "fold-c" {
		t.Fatalf("scoped variants = %+v, %v; want fold-c alone", scoped, err)
	}
	if got := lookup("!!!"); got != nil {
		t.Fatalf("variants of an empty fold = %v, want none", got)
	}
	byID, err := st.LiveFoldEqualEntitiesByID(ctx, lib.ID, "artist", "first band", []EntityID{"fold-d", "fold-b", "fold-a", "fold-missing"})
	if err != nil || len(byID) != 2 || byID[0].ID != "fold-a" || byID[1].ID != "fold-d" {
		t.Fatalf("variants by ID = %+v, %v; want fold-a then fold-d", byID, err)
	}
}

// TestMergeTitleFoldVariantsOwesItsActivityEntry pins CORE-2459's durable
// merge record: a title-fold merge commits with a row naming its batch and
// Library until the entry is marked recorded, and a manual merge leaves none.
func TestMergeTitleFoldVariantsOwesItsActivityEntry(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []EntityID{"fold-a", "fold-b", "fold-c"} {
		if err := st.UpsertEntity(ctx, Entity{ID: id, LibraryID: lib.ID, Kind: "artist", Title: "First Band"}); err != nil {
			t.Fatal(err)
		}
	}
	all := LibraryFilter{AllLibraries: true, Revealed: true}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := st.MergeEntities(ctx, "fold-a", "fold-c", all, now); err != nil {
		t.Fatal(err)
	}
	if owed, err := st.UnrecordedTitleFoldMerges(ctx); err != nil || len(owed) != 0 {
		t.Fatalf("after a manual merge owed = %+v, %v; want none", owed, err)
	}
	batch, err := st.MergeTitleFoldVariants(ctx, "fold-a", "fold-b", all, now)
	if err != nil {
		t.Fatal(err)
	}
	owed, err := st.UnrecordedTitleFoldMerges(ctx)
	want := []TitleFoldMerge{{BatchID: batch.ID, LibraryID: lib.ID, MergedAt: now}}
	if err != nil || len(owed) != 1 || owed[0].BatchID != want[0].BatchID || owed[0].LibraryID != lib.ID || !owed[0].MergedAt.Equal(now) {
		t.Fatalf("owed = %+v, %v; want %+v", owed, err, want)
	}
	if err := st.MarkTitleFoldMergeRecorded(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	if owed, err := st.UnrecordedTitleFoldMerges(ctx); err != nil || len(owed) != 0 {
		t.Fatalf("after marking owed = %+v, %v; want none", owed, err)
	}
	if _, err := st.UndoBatch(ctx, batch.ID, all, time.Now().UTC()); err != nil {
		t.Fatalf("undo title-fold merge: %v", err)
	}
}

// TestLiveFoldEqualEntitiesQueriesSeekTheirIndexes pins the cost of the
// title-fold lookup: bringing the index up to date reads only the rowid range
// added since, and a scoped lookup reads the scope's relations, so neither
// rereads a Library's whole kind on each hydration.
func TestLiveFoldEqualEntitiesQueriesSeekTheirIndexes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := st.catalogR.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return strings.Join(out, "; ")
	}
	if got := plan(titleFoldCatchUpSQL, 1, 2, "lib", "artist"); !strings.Contains(got, "INTEGER PRIMARY KEY (rowid>? AND rowid<?)") {
		t.Fatalf("catch-up plan = %q, want a rowid range search", got)
	}
	if got := plan(titleFoldScopedSQL, "scope", "member-of", "lib", "artist"); !strings.Contains(got, "entity_relation_to_entity_idx") {
		t.Fatalf("scoped plan = %q, want the relation target index", got)
	}
	if got := plan(titleFoldByIDSQL(2), "lib", "artist", "a", "b"); !strings.Contains(got, "(id=?)") || strings.Contains(got, "entity_library_kind_idx") {
		t.Fatalf("by-ID plan = %q, want a primary key search", got)
	}
}
