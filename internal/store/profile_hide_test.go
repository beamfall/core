package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

// TestMuteMediaKindHidesOneItem covers CORE-2836: a media Mute removes that
// one Media from shelves and browse for its Profile only.
func TestMuteMediaKindHidesOneItem(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"
	hidden := muteMediaInLibrary(t, st, "library-local", "hide-film")
	kept := muteMediaInLibrary(t, st, "library-local", "keep-film")

	if err := st.SetMute(ctx, profile, MuteKindMedia, string(hidden), false, true); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	shelf := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200}))
	if shelf[hidden] || !shelf[kept] {
		t.Fatalf("shelf = %v, want %s hidden and %s kept", shelf, hidden, kept)
	}
	browse, err := st.LibraryBrowse(ctx, filter, LibraryBrowseQuery{LibraryID: "library-local", ProfileID: profile, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if got := mediaIDSet(browse.Items); got[hidden] || !got[kept] {
		t.Fatalf("browse = %v, want %s hidden", got, hidden)
	}
	other := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: "profile-other", Limit: 200}))
	if !other[hidden] {
		t.Fatalf("another Profile must still see %s", hidden)
	}
	if err := st.SetMute(ctx, profile, MuteKindMedia, string(hidden), false, false); err != nil {
		t.Fatal(err)
	}
	if again := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200})); !again[hidden] {
		t.Fatalf("unhide must restore %s", hidden)
	}
}

// TestMuteDirectEntityReachesTree covers CORE-2836: hiding a container hides
// the Media of its child containers along the plugin parent relations, and
// drops it and its children from Entity browse.
func TestMuteDirectEntityReachesTree(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	st.SetMuteParentRelations(func() []string { return []string{"part-of"} })
	const profile ProfileID = "profile-default"

	episode := muteMediaInLibrary(t, st, "library-local", "tree-episode")
	other := muteMediaInLibrary(t, st, "library-local", "tree-other")
	for _, id := range []EntityID{"entity-show", "entity-season", "entity-unrelated"} {
		if err := st.UpsertEntity(ctx, widgetEntity(id, "library-local")); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-season", Name: "part-of", ToEntityID: "entity-show"}); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-season", episode)
	muteRelate(t, st, "entity-unrelated", other)
	mustReport(t, st, profile, episode, 30, 120, false)

	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-show", false, true); err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	cw := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "continue-watching", ProfileID: profile}))
	if cw[episode] {
		t.Fatalf("hidden show's episode must leave continue-watching, got %v", cw)
	}
	all := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200}))
	if all[episode] || !all[other] {
		t.Fatalf("all-items = %v, want episode hidden, other kept", all)
	}

	entities, err := st.LibraryEntities(ctx, filter, LibraryEntitiesQuery{
		LibraryID: "library-local", Kinds: []string{"widget"}, Kind: "widget",
		Browse: LibraryBrowseQuery{ProfileID: profile},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range entities.Items {
		ids[string(e.Item.ID)] = true
	}
	if ids["entity-show"] || ids["entity-season"] || !ids["entity-unrelated"] {
		t.Fatalf("entity browse = %v, want show and season hidden", ids)
	}

	// An entity-items shelf drops the hidden container's card too.
	cards := []SourceItem{{ID: "entity-show", Kind: "entity"}, {ID: "entity-unrelated", Kind: "entity"}}
	st.mu.RLock()
	kept, err := st.applyMuteFilterLocked(ctx, st.catalogR, profile, filter, cards)
	st.mu.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].ID != "entity-unrelated" {
		t.Fatalf("entity shelf = %v, want only entity-unrelated", kept)
	}
}

// TestMuteDirectEntityCrossesKinds covers CORE-2836: a container held by a
// container of another Kind (a roll-up parent) is still under the hidden one.
func TestMuteDirectEntityCrossesKinds(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	st.SetMuteParentRelations(func() []string { return []string{"held-by"} })
	const profile ProfileID = "profile-default"

	item := muteMediaInLibrary(t, st, "library-local", "cross-item")
	top := Entity{ID: "entity-top", LibraryID: "library-local", Kind: "group", Title: "Top"}
	if err := st.UpsertEntity(ctx, top); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntity(ctx, widgetEntity("entity-inner", "library-local")); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, EntityRelation{FromEntityID: "entity-inner", Name: "held-by", ToEntityID: "entity-top"}); err != nil {
		t.Fatal(err)
	}
	muteRelate(t, st, "entity-inner", item)
	if err := st.SetMute(ctx, profile, MuteKindEntity, "entity-top", false, true); err != nil {
		t.Fatal(err)
	}
	all := mediaIDSet(mustSourceItems(t, st, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200}))
	if all[item] {
		t.Fatalf("all-items = %v, want the item under the hidden container gone", all)
	}
}

// TestHideWatchedPref covers CORE-2836: with profile.hide-watched on, catalog
// shelves and unfiltered browse drop finished Media; the Profile's own lists
// and an explicit watch filter keep them.
func TestHideWatchedPref(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"
	finished := muteMediaInLibrary(t, st, "library-local", "hw-finished")
	fresh := muteMediaInLibrary(t, st, "library-local", "hw-fresh")
	mustReport(t, st, profile, finished, 120, 120, true)
	filter := LibraryFilter{AllLibraries: true}

	shelf := func() map[MediaID]bool {
		return mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200}))
	}
	browse := func(watch string) map[MediaID]bool {
		t.Helper()
		got, err := st.LibraryBrowse(ctx, filter, LibraryBrowseQuery{LibraryID: "library-local", ProfileID: profile, Watch: watch, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		return mediaIDSet(got.Items)
	}
	if !shelf()[finished] || !browse("")[finished] {
		t.Fatal("pref off: finished Media must show")
	}
	if err := st.SetClientPref(ctx, profile, ProfileHideWatchedPrefKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := shelf(); got[finished] || !got[fresh] {
		t.Fatalf("shelf with hide watched = %v", got)
	}
	if got := browse(""); got[finished] || !got[fresh] {
		t.Fatalf("browse with hide watched = %v", got)
	}
	if !browse(LibraryBrowseWatchWatched)[finished] {
		t.Fatal("explicit watched filter must override hide watched")
	}
	played := mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "recently-played", ProfileID: profile}))
	if !played[finished] {
		t.Fatal("recently-played must keep finished Media")
	}
	if !mediaIDSet(mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: "profile-other", Limit: 200}))[finished] {
		t.Fatal("another Profile is unaffected")
	}
}

// TestHideWatchedPagesAfterTheDrop covers CORE-2836: a Hide watched shelf
// pages over what is left, so finished Media never cut a See All short.
func TestHideWatchedPagesAfterTheDrop(t *testing.T) {
	ctx := context.Background()
	st := openSeeded(t)
	const profile ProfileID = "profile-default"
	filter := LibraryFilter{AllLibraries: true}
	for _, name := range []string{"page-a", "page-b", "page-c", "page-d", "page-e"} {
		muteMediaInLibrary(t, st, "library-local", name)
	}
	all := mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 200})
	if len(all) < 4 {
		t.Fatalf("seed has %d items, want at least 4", len(all))
	}
	// Finish the first two in shelf order.
	for _, item := range all[:2] {
		mustReport(t, st, profile, item.ID, 120, 120, true)
	}
	if err := st.SetClientPref(ctx, profile, ProfileHideWatchedPrefKey, "on", time.Now()); err != nil {
		t.Fatal(err)
	}
	var paged []MediaID
	for offset := 0; ; offset += 2 {
		page := mustSourceItems(t, st, filter, SourceQuery{Kind: "all-items", ProfileID: profile, Limit: 2, Offset: offset})
		for _, item := range page {
			paged = append(paged, item.ID)
		}
		if len(page) < 2 {
			break
		}
	}
	want := []MediaID{}
	for _, item := range all[2:] {
		want = append(want, item.ID)
	}
	if !slices.Equal(paged, want) {
		t.Fatalf("paged = %v, want %v", paged, want)
	}
}
