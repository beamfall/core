package store

import (
	"context"
	"testing"
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
	for _, entity := range []Entity{
		{ID: "entity-item-parent", LibraryID: "library-local", Kind: "container", Title: "Parent"},
		{ID: "entity-item-child", LibraryID: "library-local", Kind: "container", Title: "Child"},
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
	if itemState(t, st, "profile-default", ItemTarget{EntityID: parent}).Watched {
		t.Fatal("partly watched Entity reads as watched")
	}

	changed, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent}, true, itemOpenFilter)
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
	if !itemState(t, st, "profile-default", ItemTarget{EntityID: parent}).Watched {
		t.Fatal("fully watched Entity reads as unwatched")
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
	if itemState(t, st, "profile-default", ItemTarget{EntityID: parent}).Watched {
		t.Fatal("parent reads watched with an unwatched descendant")
	}
}

func TestSetItemWatchedSurvivesProjectionRebuild(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	parent, _, ids := seedItemTree(t, st)
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{EntityID: parent}, true, itemOpenFilter); err != nil {
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
	if !itemState(t, st, "profile-default", ItemTarget{EntityID: parent}).Watchlisted {
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
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{EntityID: parent}, 9, itemOpenFilter); err != nil {
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
