package store

import (
	"context"
	"testing"
	"time"
)

var itemOpenFilter = LibraryFilter{AllowedIDs: []LibraryID{"library-local"}}

// seedItemTree builds a two-level same-Kind tree in the open Library: a parent
// relating to two Media and a child relating to a third.
func seedItemTree(t *testing.T, st *Store) (EntityID, EntityID, []MediaID) {
	t.Helper()
	ctx := context.Background()
	ids := []MediaID{
		"media-alpha",
		seedHealthMedia(t, st, "library-local", "item-two", "/library/item-two.mp4", "oshash-item-two"),
		seedHealthMedia(t, st, "library-local", "item-three", "/library/item-three.mp4", "oshash-item-three"),
	}
	linked := seedHealthMedia(t, st, "library-local", "item-linked", "/library/item-linked.mp4", "oshash-item-linked")
	for _, entity := range []Entity{
		{ID: "entity-item-parent", LibraryID: "library-local", Kind: "container", Title: "Parent"},
		{ID: "entity-item-child", LibraryID: "library-local", Kind: "container", Title: "Child"},
		{ID: itemLinkedEntity, LibraryID: "library-local", Kind: "container", Title: "Linked"},
		{ID: "entity-item-other", LibraryID: "library-local", Kind: "category", Title: "Other Kind"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []EntityRelation{
		{FromEntityID: "entity-item-parent", Name: "contains", ToMediaID: ids[0]},
		{FromEntityID: "entity-item-parent", Name: "contains", ToMediaID: ids[1]},
		{FromEntityID: "entity-item-child", Name: "contains", ToMediaID: ids[2]},
		{FromEntityID: "entity-item-child", Name: "parent", ToEntityID: "entity-item-parent"},
		// A same-Kind Entity linked under an ordinary relation is not a child.
		{FromEntityID: itemLinkedEntity, Name: "related", ToEntityID: "entity-item-parent"},
		{FromEntityID: itemLinkedEntity, Name: "contains", ToMediaID: linked},
		// A different-Kind Entity pointing at the parent must not widen the tree.
		{FromEntityID: "entity-item-other", Name: "about", ToEntityID: "entity-item-parent"},
		{FromEntityID: "entity-item-other", Name: "tagged", ToMediaID: "media-archive"},
	} {
		if err := st.UpsertEntityRelation(ctx, relation); err != nil {
			t.Fatal(err)
		}
	}
	return "entity-item-parent", "entity-item-child", ids
}

// itemParentRelations is the hierarchy relation seedItemTree links a child under.
var itemParentRelations = []string{"parent"}

const itemLinkedEntity = EntityID("entity-item-linked")

func itemState(t *testing.T, st *Store, profile ProfileID, target ItemTarget) ItemState {
	t.Helper()
	state, err := st.ItemStateFor(context.Background(), profile, target, itemOpenFilter)
	if err != nil {
		t.Fatalf("ItemStateFor(%+v): %v", target, err)
	}
	return state
}

func TestSetItemWatchedMedia(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target := ItemTarget{MediaID: "media-alpha"}

	// A resume position is cleared by the mark.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 30, DurationSeconds: 120,
	}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	changed, err := st.SetItemWatched(ctx, "profile-default", target, true, itemOpenFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != "media-alpha" {
		t.Fatalf("changed = %v, want [media-alpha]", changed)
	}
	progress, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !progress.Watched || progress.PositionSeconds != 0 || progress.PlayCount != 1 {
		t.Fatalf("after mark watched: %+v", progress)
	}
	if itemState(t, st, "profile-managed", target).Watched {
		t.Fatal("another Profile reads the Media as watched")
	}

	// Marking an already-watched Media is a no-op: no second play, no event.
	changed, err = st.SetItemWatched(ctx, "profile-default", target, true, itemOpenFilter)
	if err != nil || len(changed) != 0 {
		t.Fatalf("repeat mark: changed=%v err=%v", changed, err)
	}
	progress, _, _ = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if progress.PlayCount != 1 {
		t.Fatalf("repeat mark play count = %d, want 1", progress.PlayCount)
	}

	if _, err := st.SetItemWatched(ctx, "profile-default", target, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	progress, _, _ = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if progress.Watched || progress.PositionSeconds != 0 || progress.PlayCount != 0 {
		t.Fatalf("after mark unwatched: %+v", progress)
	}
	for _, kind := range []string{"recently-played", "continue-watching"} {
		items := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: kind, ProfileID: "profile-default"})
		for _, item := range items {
			if item.ID == "media-alpha" {
				t.Fatalf("%s still lists the unwatched Media", kind)
			}
		}
	}
}

func TestSetItemWatchedUnwatchedNeverCreatesProgress(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: "media-alpha"}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || ok {
		t.Fatalf("unwatched mark created a progress row: ok=%v err=%v", ok, err)
	}
}

func TestSetItemWatchedEntityTree(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, child, ids := seedItemTree(t, st)

	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: ids[0]}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if itemState(t, st, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}).Watched {
		t.Fatal("partly watched Entity reads as watched")
	}

	changed, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, true, itemOpenFilter)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v, want the two Media not yet watched", changed)
	}
	for _, id := range ids {
		if !itemState(t, st, "profile-default", ItemTarget{MediaID: id}).Watched {
			t.Fatalf("Media %s not watched after marking its container", id)
		}
	}
	if !itemState(t, st, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}).Watched {
		t.Fatal("fully watched Entity reads as unwatched")
	}
	linked := ItemTarget{EntityID: itemLinkedEntity}
	if itemState(t, st, "profile-default", linked).Watched {
		t.Fatal("mark followed an ordinary same-Kind link into another Entity's Media")
	}
	// Clearing the parent leaves that Entity's own mark alone too.
	if _, err := st.SetItemWatched(ctx, "profile-default", linked, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if !itemState(t, st, "profile-default", linked).Watched {
		t.Fatal("clearing the parent cleared an Entity linked under an ordinary relation")
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	// With no declared parent relation the mark stays on the Entity's own Media.
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if !itemState(t, st, "profile-default", ItemTarget{MediaID: ids[2]}).Watched || itemState(t, st, "profile-default", ItemTarget{MediaID: ids[0]}).Watched {
		t.Fatal("a flat mark did not stop at the Entity's own Media")
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	// The restricted Media reached only through a different-Kind Entity is untouched.
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-default", "media-archive"); ok {
		t.Fatal("mark crossed a different-Kind relation into a hidden Library")
	}

	// Unmarking the child clears only its own Media.
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: child}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if itemState(t, st, "profile-default", ItemTarget{MediaID: ids[2]}).Watched {
		t.Fatal("child Media still watched")
	}
	if !itemState(t, st, "profile-default", ItemTarget{MediaID: ids[0]}).Watched {
		t.Fatal("parent Media lost its mark")
	}
	if itemState(t, st, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}).Watched {
		t.Fatal("parent reads watched with an unwatched descendant")
	}
}

func TestSetItemWatchedSurvivesProjectionRebuild(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, _, ids := seedItemTree(t, st)
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: ids[1]}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: ids[1]}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	live := snapshotEngagementProjections(t, st.eventsR)
	if err := st.rebuildEngagementProjections(ctx); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if name := firstDifferingProjection(live, snapshotEngagementProjections(t, st.eventsR)); name != "" {
		t.Fatalf("rebuild differs from live projection %q", name)
	}
}

func TestItemActionsRefuseHiddenTargets(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	hidden := ItemTarget{MediaID: "media-archive"}
	if _, err := st.SetItemWatched(ctx, "profile-default", hidden, true, itemOpenFilter); err == nil {
		t.Fatal("SetItemWatched accepted a hidden Media")
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", hidden, true, itemOpenFilter); err == nil {
		t.Fatal("SetItemWatchlisted accepted a hidden Media")
	}
	if err := st.SetItemRating(ctx, "profile-default", hidden, 5, itemOpenFilter); err == nil {
		t.Fatal("SetItemRating accepted a hidden Media")
	}
	if _, err := st.ItemStateFor(ctx, "profile-default", hidden, itemOpenFilter); err == nil {
		t.Fatal("ItemStateFor read a hidden Media")
	}
	if _, ok, _ := st.PlaybackProgressFor(ctx, "profile-default", "media-archive"); ok {
		t.Fatal("refused mark wrote progress")
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{}, true, itemOpenFilter); err == nil {
		t.Fatal("empty target accepted")
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: "media-alpha", EntityID: "x"}, true, itemOpenFilter); err == nil {
		t.Fatal("double target accepted")
	}
}

func TestWatchlistSource(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, _, _ := seedItemTree(t, st)
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	query := SourceQuery{Kind: "watchlist", ProfileID: "profile-default"}

	for _, target := range []ItemTarget{{MediaID: "media-alpha"}, {EntityID: parent}, {MediaID: "media-archive"}} {
		if err := st.SetItemWatchlisted(ctx, "profile-default", target, true, revealed); err != nil {
			t.Fatal(err)
		}
	}
	// Re-adding keeps one row.
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: "media-alpha"}, true, revealed); err != nil {
		t.Fatal(err)
	}
	if got := len(mustSourceItems(t, st, revealed, query)); got != 3 {
		t.Fatalf("revealed watchlist has %d items, want 3", got)
	}
	// The same list under a filter that hides the restricted Library.
	items := mustSourceItems(t, st, itemOpenFilter, query)
	if len(items) != 2 {
		t.Fatalf("watchlist = %+v, want the open Media and the Entity", items)
	}
	if !itemState(t, st, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}).Watchlisted {
		t.Fatal("Entity does not read as watchlisted")
	}
	if got := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "watchlist", ProfileID: "profile-managed"}); len(got) != 0 {
		t.Fatalf("another Profile's watchlist = %+v", got)
	}

	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: "media-alpha"}, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	items = mustSourceItems(t, st, itemOpenFilter, query)
	if len(items) != 1 || items[0].Kind != ItemTargetEntity || string(items[0].ID) != string(parent) || items[0].Title != "Parent" {
		t.Fatalf("watchlist after remove = %+v", items)
	}
}

func TestItemRating(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, _, _ := seedItemTree(t, st)
	media := ItemTarget{MediaID: "media-alpha"}
	query := SourceQuery{Kind: "rated", ProfileID: "profile-default"}

	for _, rating := range []int{8, 6} {
		if err := st.SetItemRating(ctx, "profile-default", media, rating, itemOpenFilter); err != nil {
			t.Fatal(err)
		}
		if got := itemState(t, st, "profile-default", media).Rating; got != rating {
			t.Fatalf("rating = %d, want %d", got, rating)
		}
	}
	for _, bad := range []int{-1, 11} {
		if err := st.SetItemRating(ctx, "profile-default", media, bad, itemOpenFilter); err == nil {
			t.Fatalf("rating %d accepted", bad)
		}
	}
	if got := itemState(t, st, "profile-default", media).Rating; got != 6 {
		t.Fatalf("refused rating changed the stored one to %d", got)
	}
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{EntityID: parent, ParentRelations: itemParentRelations}, 9, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	items := mustSourceItems(t, st, itemOpenFilter, query)
	if len(items) != 2 || string(items[0].ID) != string(parent) || items[0].Summary != "9/10" || items[1].ID != "media-alpha" {
		t.Fatalf("rated = %+v, want the Entity (9) then the Media (6)", items)
	}
	if got := itemState(t, st, "profile-managed", media).Rating; got != 0 {
		t.Fatalf("another Profile reads rating %d", got)
	}

	if err := st.SetItemRating(ctx, "profile-default", media, 0, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if got := itemState(t, st, "profile-default", media).Rating; got != 0 {
		t.Fatalf("cleared rating reads %d", got)
	}
	if items := mustSourceItems(t, st, itemOpenFilter, query); len(items) != 1 {
		t.Fatalf("rated after clear = %+v", items)
	}
}

func TestItemActionRowsPrunedWithTheirTargets(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms) VALUES ('profile-default', 'media', 'media-gone', 1)`,
		`INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms) VALUES ('profile-default', 'entity', 'entity-gone', 4, 1)`,
	} {
		if _, err := st.eventsW.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: "media-alpha"}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	counts, err := st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.ItemActionRows != 2 {
		t.Fatalf("pruned %d item-action rows, want 2", counts.ItemActionRows)
	}
	if !itemState(t, st, "profile-default", ItemTarget{MediaID: "media-alpha"}).Watchlisted {
		t.Fatal("prune removed a row whose target still exists")
	}
}

// A page of the watchlist counts visible items: a hidden target ahead of the
// window must not shorten the page or shift the next one.
func TestWatchlistSourcePagesVisibleItems(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, child, _ := seedItemTree(t, st)
	revealed := LibraryFilter{AllowedIDs: []LibraryID{"library-local", "library-archive"}, Revealed: true}
	// Oldest to newest; the Source lists newest first, so the hidden Media
	// sits between the two Entities and the open Media.
	for i, target := range []ItemTarget{{MediaID: "media-alpha"}, {MediaID: "media-archive"}, {EntityID: child}, {EntityID: parent}} {
		if err := st.SetItemWatchlisted(ctx, "profile-default", target, true, revealed); err != nil {
			t.Fatal(err)
		}
		kind, id, _ := target.kindAndID()
		if _, err := st.eventsW.ExecContext(ctx, `UPDATE watchlist_item SET added_at_ms = ? WHERE target_kind = ? AND target_id = ?`, i+1, kind, id); err != nil {
			t.Fatal(err)
		}
	}
	page := func(limit, offset int) []string {
		t.Helper()
		var ids []string
		for _, item := range mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "watchlist", ProfileID: "profile-default", Limit: limit, Offset: offset}) {
			ids = append(ids, string(item.ID))
		}
		return ids
	}
	if got := page(2, 0); len(got) != 2 || got[0] != string(parent) || got[1] != string(child) {
		t.Fatalf("first page = %v, want the two Entities", got)
	}
	if got := page(2, 2); len(got) != 1 || got[0] != "media-alpha" {
		t.Fatalf("second page = %v, want the open Media past the hidden one", got)
	}
	if got := page(3, 0); len(got) != 3 {
		t.Fatalf("a page spanning the hidden target = %v, want 3 visible items", got)
	}
}

// A muted target takes no page slot: the page behind it still fills, and the
// next page does not skip an item.
func TestWatchlistSourcePagesPastMutedItems(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	_, child, ids := seedItemTree(t, st)
	// Oldest to newest; the Source lists newest first, so the muted Media leads.
	for i, id := range ids {
		if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: id}, true, itemOpenFilter); err != nil {
			t.Fatal(err)
		}
		if _, err := st.eventsW.ExecContext(ctx, `UPDATE watchlist_item SET added_at_ms = ? WHERE target_id = ?`, i+1, string(id)); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetMute(ctx, "profile-default", MuteKindEntity, string(child), false, true); err != nil {
		t.Fatal(err)
	}
	page := func(offset int) []SourceItem {
		t.Helper()
		return mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "watchlist", ProfileID: "profile-default", Limit: 1, Offset: offset})
	}
	if got := page(0); len(got) != 1 || got[0].ID != ids[1] {
		t.Fatalf("first page = %v, want the newest unmuted Media", got)
	}
	if got := page(1); len(got) != 1 || got[0].ID != ids[0] {
		t.Fatalf("second page = %v, want the oldest Media", got)
	}
	if got := page(2); len(got) != 0 {
		t.Fatalf("third page = %v, want the end of the list", got)
	}
}

// An Encrypted Library keeps its relations in its own catalog, so a mute on one
// of its Entities must be resolved there.
func TestWatchlistSourceDropsMutedEncryptedItems(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	var ids []MediaID
	for _, slug := range []string{"kept", "muted"} {
		imported, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:mute-"+slug, "/enc/"+slug+".mkv"))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, imported.ID)
		if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: imported.ID}, true, filter); err != nil {
			t.Fatal(err)
		}
	}
	entity := Entity{ID: "entity-mute-encrypted", LibraryID: library, Kind: "category", Title: "Sealed Category"}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := st.encryptedDBs[library].writer.ExecContext(ctx, `
		INSERT INTO entity_relation(from_entity_id, relation, to_media_id)
		VALUES (?, 'tagged', ?)
	`, entity.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMute(ctx, "profile-default", MuteKindEntity, string(entity.ID), false, true); err != nil {
		t.Fatal(err)
	}
	got := mustSourceItems(t, st, filter, SourceQuery{Kind: "watchlist", ProfileID: "profile-default", Limit: 1})
	if len(got) != 1 || got[0].ID != ids[0] {
		t.Fatalf("watchlist = %+v, want only the unmuted Encrypted Media", got)
	}
	if got := mustSourceItems(t, st, filter, SourceQuery{Kind: "watchlist", ProfileID: "profile-default", Limit: 1, Offset: 1}); len(got) != 0 {
		t.Fatalf("second page = %+v, want the end of the list", got)
	}
}

// A row whose Entity left the catalog is dropped, not an error, by the Sources
// and by the My Data export.
func TestItemActionSourcesDropPurgedEntities(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms) VALUES ('profile-default', 'entity', 'entity-gone', 9)`,
		`INSERT INTO item_rating(profile_id, target_kind, target_id, rating, rated_at_ms) VALUES ('profile-default', 'entity', 'entity-gone', 4, 9)`,
	} {
		if _, err := st.eventsW.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: "media-alpha"}, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	if got := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "watchlist", ProfileID: "profile-default"}); len(got) != 1 {
		t.Fatalf("watchlist = %+v, want only the Media that still exists", got)
	}
	if got := mustSourceItems(t, st, itemOpenFilter, SourceQuery{Kind: "rated", ProfileID: "profile-default"}); len(got) != 0 {
		t.Fatalf("rated = %+v, want none", got)
	}
	export, err := st.ExportMyEngagement(ctx, "profile-default", itemOpenFilter)
	if err != nil {
		t.Fatalf("export with a purged Entity target: %v", err)
	}
	if len(export.Watchlist) != 1 || len(export.Ratings) != 0 {
		t.Fatalf("export watchlist=%v ratings=%v, want the one live row", export.Watchlist, export.Ratings)
	}
}

// An Encrypted Library keeps its catalog in its own plane: item actions read
// the tree there, and the orphan sweep neither mistakes its targets for purged
// ones nor runs while the plane is sealed.
func TestItemActionsReachAnEncryptedLibrary(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	library := LibraryID("library-local")
	if err := st.SetLibraryBacking(ctx, library, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	imported, _, err := st.ImportMediaFile(ctx, encryptedImport(library, "sha256:item-encrypted", "/enc/item.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	entity := Entity{ID: "entity-item-encrypted", LibraryID: library, Kind: "container", Title: "Sealed Season"}
	if err := st.UpsertEntity(ctx, entity); err != nil {
		t.Fatal(err)
	}
	if _, err := st.encryptedDBs[library].writer.ExecContext(ctx, `
		INSERT INTO entity_relation(from_entity_id, relation, to_media_id)
		VALUES (?, 'contains', ?)
	`, entity.ID, imported.ID); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	target := ItemTarget{EntityID: entity.ID}

	changed, err := st.SetItemWatched(ctx, "profile-default", target, true, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != imported.ID {
		t.Fatalf("marked %v watched, want the Entity's one Media", changed)
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", target, true, filter); err != nil {
		t.Fatal(err)
	}
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{MediaID: imported.ID}, 7, filter); err != nil {
		t.Fatal(err)
	}
	state, err := st.ItemStateFor(ctx, "profile-default", target, filter)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Watched || !state.Watchlisted {
		t.Fatalf("encrypted Entity state = %+v, want watched and watchlisted", state)
	}

	if _, err := st.eventsW.ExecContext(ctx, `INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms) VALUES ('profile-default', 'media', 'media-gone', 1)`); err != nil {
		t.Fatal(err)
	}
	rowCount := func() int {
		t.Helper()
		var n int
		if err := st.eventsR.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM watchlist_item) + (SELECT COUNT(*) FROM item_rating) + (SELECT COUNT(*) FROM playback_progress WHERE media_id = ?)`, imported.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	counts, err := st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.ItemActionRows != 1 || rowCount() != 3 {
		t.Fatalf("open-plane prune removed %+v and left %d rows, want only the orphan gone", counts, rowCount())
	}

	if _, err := st.eventsW.ExecContext(ctx, `INSERT INTO watchlist_item(profile_id, target_kind, target_id, added_at_ms) VALUES ('profile-default', 'media', 'media-gone', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, library, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	counts, err = st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (EngagementPruneCounts{}) || rowCount() != 4 {
		t.Fatalf("sealed-plane prune removed %+v and left %d rows, want nothing removed", counts, rowCount())
	}

	// A trashed Library can be restored, so its sealed plane still holds the sweep.
	if err := st.DeleteLibrary(ctx, library); err != nil {
		t.Fatal(err)
	}
	counts, err = st.PruneOrphanedEngagementRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (EngagementPruneCounts{}) || rowCount() != 4 {
		t.Fatalf("trashed-library prune removed %+v and left %d rows, want nothing removed", counts, rowCount())
	}
}

// A playback report observed before a manual mark and delivered after it must
// not undo the mark; a report observed after it still moves the row.
func TestManualMarkOutranksAnOlderPlaybackReport(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	target := ItemTarget{MediaID: "media-alpha"}
	report := func(position int, eventTime int64) {
		t.Helper()
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
			ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: position, DurationSeconds: 120, EventTimeMS: eventTime,
		}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
	}
	before := timeToMillis(time.Now().UTC())
	report(30, before-2000)
	if _, err := st.SetItemWatched(ctx, "profile-default", target, true, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	report(45, before-1000)
	if state := itemState(t, st, "profile-default", target); !state.Watched {
		t.Fatal("a report older than the watched mark undid it")
	}
	if _, err := st.SetItemWatched(ctx, "profile-default", target, false, itemOpenFilter); err != nil {
		t.Fatal(err)
	}
	report(45, before-1000)
	progress, _, err := st.PlaybackProgressFor(ctx, "profile-default", "media-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if progress.PositionSeconds != 0 {
		t.Fatalf("a report older than the unwatched mark restored position %v", progress.PositionSeconds)
	}
	// A finished report that predates the unwatched mark must not bring back
	// the play it cleared.
	if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{
		ProfileID: "profile-default", MediaID: "media-alpha", PositionSeconds: 120, DurationSeconds: 120, EventTimeMS: before - 500,
	}, true, ResumePolicy{Class: MediaClassVideo}); err != nil {
		t.Fatal(err)
	}
	if progress, _, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil {
		t.Fatal(err)
	}
	if progress.Watched || progress.PlayCount != 0 {
		t.Fatalf("a finished report older than the unwatched mark restored the play: %+v", progress)
	}
	report(50, timeToMillis(time.Now().UTC())+60000)
	if progress, _, err = st.PlaybackProgressFor(ctx, "profile-default", "media-alpha"); err != nil || progress.PositionSeconds != 50 {
		t.Fatalf("a report newer than the mark = %+v, %v; want position 50", progress, err)
	}
}
