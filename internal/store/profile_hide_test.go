package store

import (
	"context"
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
}

// TestHideWatchedPref covers CORE-2836: with profile.hideWatched on, catalog
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
	if err := st.SetClientPref(ctx, profile, ProfileHideWatchedPrefKey, "1", time.Now()); err != nil {
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
