package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestEntityRelationTargets pins the read scoped hydration identity decides
// with: an absent Entity reports absent, an existing one reports exactly its
// entity targets of the asked relation.
func TestEntityRelationTargets(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []EntityID{"entity-box", "entity-maker-a", "entity-maker-b"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}

	targets, exists, err := st.EntityRelationTargets(ctx, "library-local", "entity-absent", "made-by")
	if err != nil || exists || len(targets) != 0 {
		t.Fatalf("absent entity = %v, %v, %v; want no targets, absent", targets, exists, err)
	}
	targets, exists, err = st.EntityRelationTargets(ctx, "library-local", "entity-box", "made-by")
	if err != nil || !exists || len(targets) != 0 {
		t.Fatalf("unrelated entity = %v, %v, %v; want no targets, present", targets, exists, err)
	}

	for _, relation := range []EntityRelation{
		{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-maker-b"},
		{FromEntityID: "entity-box", Name: "made-by", ToEntityID: "entity-maker-a"},
		{FromEntityID: "entity-box", Name: "sold-by", ToEntityID: "entity-maker-b"},
		{FromEntityID: "entity-box", Name: "holds", ToMediaID: "media-alpha"},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	targets, exists, err = st.EntityRelationTargets(ctx, "library-local", "entity-box", "made-by")
	if err != nil || !exists {
		t.Fatalf("EntityRelationTargets = %v, %v", exists, err)
	}
	if want := []EntityID{"entity-maker-a", "entity-maker-b"}; !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %v, want %v", targets, want)
	}
	// Another Library's id never resolves here.
	if _, exists, err = st.EntityRelationTargets(ctx, "library-archive", "entity-box", "made-by"); err != nil || exists {
		t.Fatalf("entity seen through another library: %v, %v", exists, err)
	}
}

// TestSplitAmbiguousEntityScopesRequeuesMembersOnce pins the one-time repair:
// an Entity whose scope relation points at two Entities loses those rows, and
// its member Media (like those of an Entity that records no scope at all) lose
// the hydration marker so the next scan hydrates them again; an Entity with
// one scope and another plugin's Entities are untouched; and a second run
// changes nothing, even for an Entity that became ambiguous afterwards.
func TestSplitAmbiguousEntityScopesRequeuesMembersOnce(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Shelf", false, "shelf", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateLibrary(ctx, "Other", false, "other", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	member := func(library LibraryID, name string) MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
			LibraryID: library, Path: "/" + string(library) + "/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, media.ID, []MediaField{{Name: "hydrated", Type: EntityFieldFlag, Flag: true}}); err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	mergedA, mergedB := member(lib.ID, "merged-a"), member(lib.ID, "merged-b")
	unscoped, single, foreign := member(lib.ID, "unscoped"), member(lib.ID, "single"), member(other.ID, "foreign")
	for _, id := range []EntityID{"entity-merged", "entity-unscoped", "entity-single", "entity-maker-a", "entity-maker-b"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, lib.ID)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntity(ctx, widgetEntity("entity-foreign", other.ID)); err != nil {
		t.Fatal(err)
	}
	for _, relation := range []EntityRelation{
		{FromEntityID: "entity-merged", Name: "holds", ToMediaID: mergedA},
		{FromEntityID: "entity-merged", Name: "holds", ToMediaID: mergedB},
		{FromEntityID: "entity-merged", Name: "made-by", ToEntityID: "entity-maker-a"},
		{FromEntityID: "entity-merged", Name: "made-by", ToEntityID: "entity-maker-b"},
		{FromEntityID: "entity-unscoped", Name: "holds", ToMediaID: unscoped},
		{FromEntityID: "entity-single", Name: "holds", ToMediaID: single},
		{FromEntityID: "entity-single", Name: "made-by", ToEntityID: "entity-maker-a"},
		{FromEntityID: "entity-foreign", Name: "holds", ToMediaID: foreign},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	stale := func(library LibraryID) map[MediaID]struct{} {
		t.Helper()
		lacking, err := st.MediaLackingFieldInLibrary(ctx, library, MediaClassAudio, "hydrated")
		if err != nil {
			t.Fatal(err)
		}
		return lacking
	}
	targets := func(id EntityID) []EntityID {
		t.Helper()
		out, exists, err := st.EntityRelationTargets(ctx, lib.ID, id, "made-by")
		if err != nil || !exists {
			t.Fatalf("EntityRelationTargets(%s) = %v, %v", id, exists, err)
		}
		return out
	}

	// The scope targets are widgets too: they record no scope of their own, so
	// they are reopened as well, which is harmless (they have no member Media).
	reopened, err := st.SplitAmbiguousEntityScopes(ctx, "shelf", "widget", "made-by", "hydrated", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if reopened != 4 {
		t.Fatalf("reopened = %d, want merged, unscoped and the two makers", reopened)
	}
	if want := map[MediaID]struct{}{mergedA: {}, mergedB: {}, unscoped: {}}; !reflect.DeepEqual(stale(lib.ID), want) {
		t.Fatalf("stale Media = %v, want the ambiguous entities' members %v", stale(lib.ID), want)
	}
	if got := stale(other.ID); len(got) != 0 {
		t.Fatalf("another plugin's Media were requeued: %v", got)
	}
	// Only a backfill scan hydrates an unchanged file again, so the Library
	// whose Media were reopened awaits one until it is queued.
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || !reflect.DeepEqual(pending, []LibraryID{lib.ID}) {
		t.Fatalf("pending backfills = %v, %v; want %s", pending, err, lib.ID)
	}
	if err := st.ClearEntityScopeBackfill(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending backfills after clear = %v, %v; want none", pending, err)
	}
	if got := targets("entity-merged"); len(got) != 0 {
		t.Fatalf("merged entity still records scopes %v", got)
	}
	if got := targets("entity-single"); !reflect.DeepEqual(got, []EntityID{"entity-maker-a"}) {
		t.Fatalf("single-scope entity targets = %v, want untouched", got)
	}
	// The members stay related: nothing is orphaned while they wait for the scan.
	media, err := st.MediaForEntity(ctx, "entity-merged", LibraryFilter{AllLibraries: true})
	if err != nil || len(media) != 2 {
		t.Fatalf("merged entity members = %d, %v; want both kept", len(media), err)
	}

	// A relation added afterwards (by hand, say) is never undone.
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-single", Name: "made-by", ToEntityID: "entity-maker-b"}); err != nil {
		t.Fatal(err)
	}
	reopened, err = st.SplitAmbiguousEntityScopes(ctx, "shelf", "widget", "made-by", "hydrated", time.Now().UTC())
	if err != nil || reopened != 0 {
		t.Fatalf("second run = %d, %v; want a no-op", reopened, err)
	}
	if got := targets("entity-single"); len(got) != 2 {
		t.Fatalf("second run undid a later relation: %v", got)
	}
	if _, requeued := stale(lib.ID)[single]; requeued {
		t.Fatal("second run cleared a hydration marker")
	}
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("second run queued backfills %v, %v", pending, err)
	}
}
