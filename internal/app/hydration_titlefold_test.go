package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/artwork"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
)

// foldedAlbumMapper is scopedAlbumMapper with the container's title read from
// the "folder" tag, the way a plugin that names a container after its folder
// settles one spelling, and both kinds declared as title-folding.
type foldedAlbumMapper struct{ scopedAlbumMapper }

func (foldedAlbumMapper) TitleFoldKinds() []string { return []string{"album", "artist"} }

func (foldedAlbumMapper) MapHydration(metadata map[string]string, path string) HydrationPlan {
	plan := scopedAlbumMapper{}.MapHydration(map[string]string{"album": metadata["album"], "album-artist": metadata["folder"]}, path)
	plan.MarkComplete = true
	return plan
}

// TestTitleFoldRepairMergesSplitEntitiesOnRescan pins CORE-2459's rescan path:
// a library hydrated while each file's own spelling named its container
// ("First Band", "first.band", "FIRST BAND") holds three parents and three
// scoped children. The one-time repair reopens their Media, the next scan
// hydrates them under the one settled spelling, and the newer variants merge
// into the oldest one, which keeps its ID (and so its artwork) and its
// spelling: one parent and one child hold every Media, the losers are trashed
// (recoverable), a rating and a watchlist entry recorded on a loser carry over,
// and each merge is in activity history with the batch that undoes it.
func TestTitleFoldRepairMergesSplitEntitiesOnRescan(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	spelling := map[store.MediaID]string{}
	// The split, oldest first: every spelling hydrated into its own parent
	// and child.
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	for _, file := range []struct{ name, artist string }{{"one", "First Band"}, {"two", "first.band"}, {"three", "FIRST BAND"}} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + file.name + ".flac", Title: file.name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + file.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		spelling[media.ID] = file.artist
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: media.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": file.artist},
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, media.ID, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
			t.Fatal(err)
		}
	}
	entities := func(kind string) []store.Entity {
		t.Helper()
		out, err := st.EntitiesByKind(ctx, lib.ID, kind, all, store.EntityKindOptions{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	artists, albums := entities("artist"), entities("album")
	if len(artists) != 3 || len(albums) != 3 {
		t.Fatalf("before repair: %d parents, %d children; want the three-way split", len(artists), len(albums))
	}
	var loserArtist, loserAlbum, establishedArtist store.EntityID
	for _, artist := range artists {
		switch artist.Title {
		case "first.band":
			loserArtist = artist.ID
		case "First Band":
			establishedArtist = artist.ID
		}
	}
	for _, album := range albums {
		targets, _, _ := st.EntityRelationTargets(ctx, lib.ID, album.ID, "album-artist")
		if len(targets) == 1 && targets[0] == loserArtist {
			loserAlbum = album.ID
		}
	}
	if loserArtist == "" || loserAlbum == "" {
		t.Fatalf("split entities not found: parent %q child %q", loserArtist, loserAlbum)
	}
	const profile = store.ProfileID("profile-default")
	if err := st.SetItemRating(ctx, profile, store.ItemTarget{EntityID: loserArtist}, 8, all); err != nil {
		t.Fatal(err)
	}
	if err := st.SetItemWatchlisted(ctx, profile, store.ItemTarget{EntityID: loserAlbum}, true, all); err != nil {
		t.Fatal(err)
	}

	// The repair reopens every Media under the fold-equal parents, once.
	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	hydrators := map[string]scanpkg.MetadataHydrator{testHydrationPluginID: adapter}
	mappers := map[string]HydrationMapper{testHydrationPluginID: foldedAlbumMapper{}}
	splitAmbiguousHydrationScopes(ctx, st, hydrators, mappers, slog.New(slog.DiscardHandler))
	stale, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField)
	if err != nil || len(stale) != len(spelling) {
		t.Fatalf("stale after repair = %v, %v; want every Media reopened", stale, err)
	}
	// A library with no root keeps its backfill mark until a scan can be queued.
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 1 || pending[0] != lib.ID {
		t.Fatalf("pending backfills = %v, %v; want %s", pending, err, lib.ID)
	}
	if reopened, err := st.ReopenFoldedEntityTitles(ctx, testHydrationPluginID, "artist", testHydrationMarkerField, time.Now().UTC()); err != nil || reopened != 0 {
		t.Fatalf("second repair = %d, %v; want a no-op", reopened, err)
	}

	// The backfill scan hydrates them under the folder's spelling.
	for media := range stale {
		if err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	artists, albums = entities("artist"), entities("album")
	if len(artists) != 1 || artists[0].Title != "First Band" || artists[0].ID != establishedArtist {
		t.Fatalf("after rescan parents = %+v, want the established \"First Band\" %s", artists, establishedArtist)
	}
	if len(albums) != 1 {
		t.Fatalf("after rescan children = %+v, want one", albums)
	}
	members, err := st.MediaForEntity(ctx, albums[0].ID, all)
	if err != nil || len(members) != len(spelling) {
		t.Fatalf("child members = %d, %v; want every Media", len(members), err)
	}
	targets, _, err := st.EntityRelationTargets(ctx, lib.ID, albums[0].ID, "album-artist")
	if err != nil || len(targets) != 1 || targets[0] != artists[0].ID {
		t.Fatalf("child scope = %v, %v; want the one parent %s", targets, err, artists[0].ID)
	}
	if _, err := st.Entity(ctx, loserArtist, all); err == nil {
		t.Fatalf("loser parent %s still live", loserArtist)
	}
	if state, err := st.ItemStateFor(ctx, profile, store.ItemTarget{EntityID: artists[0].ID}, all); err != nil || state.Rating != 8 {
		t.Fatalf("parent state = %+v, %v; want the loser's rating 8", state, err)
	}
	if state, err := st.ItemStateFor(ctx, profile, store.ItemTarget{EntityID: albums[0].ID}, all); err != nil || !state.Watchlisted {
		t.Fatalf("child state = %+v, %v; want the loser's watchlist entry", state, err)
	}
	// Four merges (two parents, two children), each in activity history.
	if merges := titleFoldMerges(t, st); len(merges) != 4 {
		t.Fatalf("title-fold merge activity = %+v, want four", merges)
	}
	// A later file still spelling a merged-away title joins the live Entities,
	// not the trashed ones.
	late, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/four.flac", Title: "four", Container: "flac", Codec: "flac",
		Class: store.MediaClassAudio, ContentFingerprint: "fp-four",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
		MediaID: late.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "first.band"},
	}); err != nil {
		t.Fatal(err)
	}
	if members, err := st.MediaForEntity(ctx, albums[0].ID, all); err != nil || len(members) != len(spelling)+1 {
		t.Fatalf("live child members = %d, %v; want the late Media joined", len(members), err)
	}
	if got := entities("artist"); len(got) != 1 {
		t.Fatalf("after a late variant parents = %+v, want one", got)
	}
	// The losing parent sits in trash and restores.
	if err := st.RestoreEntity(ctx, loserArtist, all); err != nil {
		t.Fatalf("restore loser parent: %v", err)
	}
	if restored, err := st.Entity(ctx, loserArtist, all); err != nil || restored.Title != "first.band" {
		t.Fatalf("restored loser = %+v, %v", restored, err)
	}
}

// recordingFoldStore fails the first failCopies item-state copies and the
// first failAppends activity entries, records every merge batch, and counts
// the variant lookups that list a whole kind, by kind, so a test can retry a
// failed merge or record, undo a good one, and see what a lookup cost.
type recordingFoldStore struct {
	hydrationFoldStore
	failCopies     int
	failAppends    int
	batches        []store.BulkOpBatch
	kindWideLookup map[string]int
}

func (r *recordingFoldStore) AppendActivity(ctx context.Context, entry store.ActivityEntry) error {
	if r.failAppends > 0 {
		r.failAppends--
		return errors.New("append failed")
	}
	return r.hydrationFoldStore.AppendActivity(ctx, entry)
}

func (r *recordingFoldStore) LiveFoldEqualEntities(ctx context.Context, libraryID store.LibraryID, kind, title, scopeRelation string, scopeTarget store.EntityID) ([]store.Entity, error) {
	if scopeRelation == "" {
		if r.kindWideLookup == nil {
			r.kindWideLookup = map[string]int{}
		}
		r.kindWideLookup[kind]++
	}
	return r.hydrationFoldStore.LiveFoldEqualEntities(ctx, libraryID, kind, title, scopeRelation, scopeTarget)
}

func (r *recordingFoldStore) CopyEntityItemState(ctx context.Context, from, to store.EntityID) error {
	if r.failCopies > 0 {
		r.failCopies--
		return errors.New("copy failed")
	}
	return r.hydrationFoldStore.CopyEntityItemState(ctx, from, to)
}

func (r *recordingFoldStore) MergeTitleFoldVariants(ctx context.Context, winner, loser store.EntityID, filter store.LibraryFilter, now time.Time) (store.BulkOpBatch, error) {
	batch, err := r.hydrationFoldStore.MergeTitleFoldVariants(ctx, winner, loser, filter, now)
	if err == nil {
		r.batches = append(r.batches, batch)
	}
	return batch, err
}

// TestTitleFoldMergeRetriesAfterFailureAndUndoRestoresTheMedia pins CORE-2459's
// failure and undo paths for a newer variant holding a single Media: a failed
// merge fails the hydration with the Media still on its previous holder, the
// next hydration merges it into the established variant and records each merge
// in activity history (parent first) with the batch that undoes it, and undoing
// those batches newest first gives the old holder its Media back.
func TestTitleFoldMergeRetriesAfterFailureAndUndoRestoresTheMedia(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	// "First Band" is established; "first.band" came later and holds media.
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	var media store.Media
	for _, file := range []struct{ name, artist string }{{"one", "First Band"}, {"two", "first.band"}} {
		imported, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + file.name + ".flac", Title: file.name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + file.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: imported.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": file.artist},
		}); err != nil {
			t.Fatal(err)
		}
		media = imported
	}
	links, err := st.MediaHolderLinks(ctx, lib.ID, media.ID)
	if err != nil {
		t.Fatal(err)
	}
	var oldHolder store.EntityID
	for _, link := range links {
		if link.Kind == "album" {
			oldHolder = link.EntityID
		}
	}
	if oldHolder == "" {
		t.Fatalf("legacy holders = %+v; want a child holding the Media", links)
	}

	// The Media was hydrated before, so it carries its marker.
	if err := st.InsertMediaFieldsIfAbsent(ctx, media.ID, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
		t.Fatal(err)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore, failCopies: 1}
	adapter.foldStore = recorder
	input := scanpkg.MetadataHydrationInput{
		MediaID: media.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
	}
	if err := adapter.HydrateMetadata(ctx, input); err == nil {
		t.Fatal("hydration with a failing merge succeeded")
	}
	if members, err := st.MediaForEntity(ctx, oldHolder, all); err != nil || len(members) != 1 {
		t.Fatalf("after failed merge the old holder has %d members, %v; want the Media kept", len(members), err)
	}
	if open, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField); err != nil {
		t.Fatal(err)
	} else if _, ok := open[media.ID]; !ok {
		t.Fatalf("after failed merge unmarked Media = %v; want %s left for the next scan", open, media.ID)
	}
	// An ordinary scan skips the unchanged file, so a backfill is queued.
	if pending, err := st.PendingEntityScopeBackfills(ctx); err != nil || len(pending) != 1 || pending[0] != lib.ID {
		t.Fatalf("pending backfills = %v, %v; want %s", pending, err, lib.ID)
	}
	if err := adapter.HydrateMetadata(ctx, input); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := st.Entity(ctx, oldHolder, all); err == nil {
		t.Fatalf("old holder %s still live after the retried merge", oldHolder)
	}
	if len(recorder.batches) != 2 {
		t.Fatalf("merge batches = %d, want the parent and the child", len(recorder.batches))
	}
	// Ordering a scoped child's variants reads their rows, never the kind.
	if n := recorder.kindWideLookup["album"]; n != 0 {
		t.Fatalf("kind-wide lookups of the scoped kind = %d, want none", n)
	}
	// History lists the merges newest first: the child's, then the parent's.
	merges := titleFoldMerges(t, st)
	if len(merges) != 2 {
		t.Fatalf("title-fold merge activity = %+v, want two", merges)
	}
	for i, entry := range merges {
		want := recorder.batches[len(recorder.batches)-1-i].ID
		if entry.TargetType != "batch" || entry.TargetID != want || entry.LibraryID != lib.ID || entry.ProfileID != "" || entry.OperationID == "" {
			t.Fatalf("merge activity %d = %+v, want batch %s in %s with no actor", i, entry, want, lib.ID)
		}
	}
	for _, entry := range merges {
		if _, err := st.UndoBatch(ctx, entry.TargetID, all, time.Now().UTC()); err != nil {
			t.Fatalf("undo merge: %v", err)
		}
	}
	if members, err := st.MediaForEntity(ctx, oldHolder, all); err != nil || len(members) != 1 || members[0].ID != media.ID {
		t.Fatalf("after undo the old holder has %v, %v; want the Media back", members, err)
	}
}

// TestTitleFoldNewSpellingJoinsTheEstablishedVariant pins CORE-2459's
// upgrade path for a container no older scan split: every file hydrated under
// one file-name spelling, so the one-time repair has nothing to reopen. A new
// file named by the folder's spelling joins the existing parent and child
// instead of creating twins beside them: they keep their IDs (and so their
// artwork), their spelling and their item state, and nothing is merged.
func TestTitleFoldNewSpellingJoinsTheEstablishedVariant(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	importMedia := func(name string) store.MediaID {
		t.Helper()
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		return media.ID
	}
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	for _, name := range []string{"one", "two"} {
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: importMedia(name), LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": "first.band"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	entities := func(kind string) []store.Entity {
		t.Helper()
		out, err := st.EntitiesByKind(ctx, lib.ID, kind, all, store.EntityKindOptions{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	oldArtists, oldAlbums := entities("artist"), entities("album")
	if len(oldArtists) != 1 || len(oldAlbums) != 1 {
		t.Fatalf("legacy parents %+v, children %+v; want one of each", oldArtists, oldAlbums)
	}
	const profile = store.ProfileID("profile-default")
	if err := st.SetItemRating(ctx, profile, store.ItemTarget{EntityID: oldArtists[0].ID}, 7, all); err != nil {
		t.Fatal(err)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore}
	adapter.foldStore = recorder
	three := importMedia("three")
	if err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
		MediaID: three, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
	}); err != nil {
		t.Fatal(err)
	}
	artists, albums := entities("artist"), entities("album")
	if len(artists) != 1 || artists[0].ID != oldArtists[0].ID || artists[0].Title != "first.band" {
		t.Fatalf("parents = %+v, want the established %+v", artists, oldArtists[0])
	}
	if len(albums) != 1 || albums[0].ID != oldAlbums[0].ID {
		t.Fatalf("children = %+v, want the established %+v", albums, oldAlbums[0])
	}
	if members, err := st.MediaForEntity(ctx, albums[0].ID, all); err != nil || len(members) != 3 {
		t.Fatalf("child members = %d, %v; want all three Media", len(members), err)
	}
	if state, err := st.ItemStateFor(ctx, profile, store.ItemTarget{EntityID: artists[0].ID}, all); err != nil || state.Rating != 7 {
		t.Fatalf("parent state = %+v, %v; want the rating 7 kept", state, err)
	}
	if len(recorder.batches) != 0 {
		t.Fatalf("merge batches = %+v, want none", recorder.batches)
	}
	if merges := titleFoldMerges(t, st); len(merges) != 0 {
		t.Fatalf("title-fold merge activity = %+v, want none", merges)
	}
}

// TestTitleFoldKeepsAChildThatOnlyTheNewerParentHeld pins CORE-2459 for
// children that differ between two spellings of one parent: "First Band"
// holds "One", the newer "first.band" holds "Two". Rehydrated under the
// settled spelling, in either order, the parents merge and both children
// survive under the established parent with their own IDs: "Two", whose id
// derives from the merged-away spelling, is reused rather than twinned. The
// first parent merge fails: the retry neither twins "Two" nor leaves it under
// two parents, and undoing the merge puts each child back under its own
// spelling alone.
func TestTitleFoldKeepsAChildThatOnlyTheNewerParentHeld(t *testing.T) {
	for _, order := range [][]int{{0, 1}, {1, 0}} {
		ctx := context.Background()
		st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
		if err != nil {
			t.Fatal(err)
		}
		lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		all := store.LibraryFilter{AllLibraries: true, Revealed: true}
		files := []struct {
			name, album, artist string
			media               store.MediaID
		}{{name: "one", album: "One", artist: "First Band"}, {name: "two", album: "Two", artist: "first.band"}}
		legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
		for i := range files {
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
				LibraryID: lib.ID, Path: "/shelf/" + files[i].name + ".flac", Title: files[i].name, Container: "flac", Codec: "flac",
				Class: store.MediaClassAudio, ContentFingerprint: "fp-" + files[i].name,
			})
			if err != nil {
				t.Fatal(err)
			}
			files[i].media = media.ID
			if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
				MediaID: media.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
				Metadata: map[string]string{"album": files[i].album, "album-artist": files[i].artist},
			}); err != nil {
				t.Fatal(err)
			}
		}
		entities := func(kind string) map[string]store.EntityID {
			t.Helper()
			out, err := st.EntitiesByKind(ctx, lib.ID, kind, all, store.EntityKindOptions{}, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			byTitle := map[string]store.EntityID{}
			for _, entity := range out {
				if _, dup := byTitle[entity.Title]; dup {
					t.Fatalf("order %v: two live %s Entities titled %q: %+v", order, kind, entity.Title, out)
				}
				byTitle[entity.Title] = entity.ID
			}
			return byTitle
		}
		parents, children := entities("artist"), entities("album")
		if len(parents) != 2 || len(children) != 2 {
			t.Fatalf("legacy parents %v, children %v; want two of each", parents, children)
		}
		adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
		adapter.foldStore = &recordingFoldStore{hydrationFoldStore: adapter.foldStore, failCopies: 1}
		hydrateErr := func(i int) error {
			return adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
				MediaID: files[i].media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
				Metadata: map[string]string{"album": files[i].album, "folder": "First Band"},
			})
		}
		hydrate := func(i int) {
			t.Helper()
			if err := hydrateErr(i); err != nil {
				t.Fatalf("order %v: hydrate %s: %v", order, files[i].name, err)
			}
		}
		if err := hydrateErr(order[0]); err == nil {
			t.Fatalf("order %v: hydration with a failing parent merge succeeded", order)
		}
		for _, i := range order {
			hydrate(i)
		}
		// A later scan, with nothing remembered, changes nothing.
		adapter = newTestAdapterWithMapper(st, foldedAlbumMapper{})
		for _, i := range order {
			hydrate(i)
		}
		gotParents, gotChildren := entities("artist"), entities("album")
		if len(gotParents) != 1 || gotParents["First Band"] != parents["First Band"] {
			t.Fatalf("order %v: parents = %v, want the established %s", order, gotParents, parents["First Band"])
		}
		if len(gotChildren) != 2 || gotChildren["One"] != children["One"] || gotChildren["Two"] != children["Two"] {
			t.Fatalf("order %v: children = %v, want %v kept", order, gotChildren, children)
		}
		for i, file := range files {
			id := gotChildren[file.album]
			if targets, _, err := st.EntityRelationTargets(ctx, lib.ID, id, "album-artist"); err != nil || len(targets) != 1 || targets[0] != parents["First Band"] {
				t.Fatalf("order %v: %s scope = %v, %v; want the established parent", order, file.album, targets, err)
			}
			if members, err := st.MediaForEntity(ctx, id, all); err != nil || len(members) != 1 || members[0].ID != files[i].media {
				t.Fatalf("order %v: %s members = %+v, %v; want its own Media", order, file.album, members, err)
			}
		}
		merges := titleFoldMerges(t, st)
		if len(merges) != 1 {
			t.Fatalf("order %v: title-fold merge activity = %+v, want the parent's alone", order, merges)
		}
		for _, entry := range merges {
			if _, err := st.UndoBatch(ctx, entry.TargetID, all, time.Now().UTC()); err != nil {
				t.Fatalf("order %v: undo %s: %v", order, entry.TargetID, err)
			}
		}
		for _, file := range files {
			if targets, _, err := st.EntityRelationTargets(ctx, lib.ID, children[file.album], "album-artist"); err != nil || len(targets) != 1 || targets[0] != parents[file.artist] {
				t.Fatalf("order %v: %s scope after undo = %v, %v; want %s alone", order, file.album, targets, err, parents[file.artist])
			}
		}
		_ = st.Close()
	}
}

// TestTitleFoldMergeOwesItsActivityEntryUntilWritten pins CORE-2459's merge
// record: when the activity entry of a committed merge fails, the hydration
// fails, the next one writes the entry before merging again, and a merge left
// unrecorded by a process that stopped is recorded by the next adapter, so
// every merge stays findable and undoable from history.
func TestTitleFoldMergeOwesItsActivityEntryUntilWritten(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	var media store.MediaID
	for _, file := range []struct{ name, artist string }{{"one", "First Band"}, {"two", "first.band"}} {
		imported, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + file.name + ".flac", Title: file.name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + file.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: imported.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": file.artist},
		}); err != nil {
			t.Fatal(err)
		}
		media = imported.ID
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore, failAppends: 1}
	adapter.foldStore = recorder
	input := scanpkg.MetadataHydrationInput{
		MediaID: media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
	}
	if err := adapter.HydrateMetadata(ctx, input); err == nil {
		t.Fatal("hydration with a failing activity entry succeeded")
	}
	if len(recorder.batches) != 1 {
		t.Fatalf("merge batches = %+v, want the parent's", recorder.batches)
	}
	if owed, err := st.UnrecordedTitleFoldMerges(ctx); err != nil || len(owed) != 1 || owed[0].BatchID != recorder.batches[0].ID || owed[0].LibraryID != lib.ID {
		t.Fatalf("owed = %+v, %v; want the parent's merge", owed, err)
	}
	if merges := titleFoldMerges(t, st); len(merges) != 0 {
		t.Fatalf("activity after the failed entry = %+v, want none", merges)
	}
	if err := adapter.HydrateMetadata(ctx, input); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if owed, err := st.UnrecordedTitleFoldMerges(ctx); err != nil || len(owed) != 0 {
		t.Fatalf("owed after retry = %+v, %v; want none", owed, err)
	}
	merges := titleFoldMerges(t, st)
	if len(merges) != 2 || len(recorder.batches) != 2 {
		t.Fatalf("activity = %+v, batches = %+v; want the parent and the child", merges, recorder.batches)
	}
	for i, entry := range merges {
		if want := recorder.batches[len(recorder.batches)-1-i].ID; entry.TargetID != want {
			t.Fatalf("merge activity %d = %+v, want batch %s", i, entry, want)
		}
	}

	// A merge committed by a process that stopped before its entry: the next
	// adapter records it, at the merge's time, before it hydrates anything.
	// Activity lists newest entry first, so the late entry leads.
	stranded := store.EntityID("stranded-variant")
	if err := st.UpsertEntity(ctx, store.Entity{ID: stranded, LibraryID: lib.ID, Kind: "artist", Title: "FIRST BAND"}); err != nil {
		t.Fatal(err)
	}
	var winner store.EntityID
	parents, err := st.EntitiesByKind(ctx, lib.ID, "artist", all, store.EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range parents {
		if parent.ID != stranded {
			winner = parent.ID
		}
	}
	mergedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	batch, err := st.MergeTitleFoldVariants(ctx, winner, stranded, all, mergedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := newTestAdapterWithMapper(st, foldedAlbumMapper{}).HydrateMetadata(ctx, input); err != nil {
		t.Fatal(err)
	}
	if owed, err := st.UnrecordedTitleFoldMerges(ctx); err != nil || len(owed) != 0 {
		t.Fatalf("owed after the next adapter = %+v, %v; want none", owed, err)
	}
	merges = titleFoldMerges(t, st)
	if len(merges) != 3 || merges[0].TargetID != batch.ID || !merges[0].OccurredAt.Equal(mergedAt) || merges[0].LibraryID != lib.ID {
		t.Fatalf("activity = %+v, want the stranded merge %s recorded at %v", merges, batch.ID, mergedAt)
	}
	for _, entry := range merges {
		if _, err := st.UndoBatch(ctx, entry.TargetID, all, time.Now().UTC()); err != nil {
			t.Fatalf("undo %s: %v", entry.TargetID, err)
		}
	}
}

// failingFoldArtwork fails the first failures artwork copies, then hands
// them to the real artwork store.
type failingFoldArtwork struct {
	hydrationFoldArtwork
	failures int
}

func (f *failingFoldArtwork) AdoptEntityArtwork(ctx context.Context, survivor, merged store.EntityID) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("artwork copy failed")
	}
	return f.hydrationFoldArtwork.AdoptEntityArtwork(ctx, survivor, merged)
}

// TestTitleFoldMergeKeepsArtworkOnTheSurvivor pins CORE-2459's artwork rule:
// the poster of the merged-away parent spelling ends up on the established
// parent, which had none, and stays on the trashed variant for an undo. A
// copy that fails fails the hydration before anything merges, so the retry
// merges and copies.
func TestTitleFoldMergeKeepsArtworkOnTheSurvivor(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	var media store.MediaID
	for _, file := range []struct{ name, artist string }{{"one", "First Band"}, {"two", "first.band"}} {
		imported, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + file.name + ".flac", Title: file.name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + file.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: imported.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": file.artist},
		}); err != nil {
			t.Fatal(err)
		}
		media = imported.ID
	}
	parents, err := st.EntitiesByKind(ctx, lib.ID, "artist", all, store.EntityKindOptions{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var established, newer store.EntityID
	for _, parent := range parents {
		if parent.Title == "First Band" {
			established = parent.ID
		} else {
			newer = parent.ID
		}
	}
	art := artwork.NewService(st, nil, t.TempDir())
	poster := art.EntityPosterPath(newer)
	if err := os.MkdirAll(filepath.Dir(poster), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poster, []byte("poster of the newer spelling"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore}
	adapter.foldStore = recorder
	setHydrationFoldArtwork(map[string]scanpkg.MetadataHydrator{testHydrationPluginID: adapter}, &failingFoldArtwork{hydrationFoldArtwork: art, failures: 1})
	input := scanpkg.MetadataHydrationInput{
		MediaID: media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
	}
	if err := adapter.HydrateMetadata(ctx, input); err == nil {
		t.Fatal("hydration with a failing artwork copy succeeded")
	}
	if len(recorder.batches) != 0 || art.HasEntityPoster(established) {
		t.Fatalf("failed copy merged %+v or copied a poster", recorder.batches)
	}
	if err := adapter.HydrateMetadata(ctx, input); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(recorder.batches) == 0 {
		t.Fatal("retry merged nothing")
	}
	for _, id := range []store.EntityID{established, newer} {
		if got, err := os.ReadFile(art.EntityPosterPath(id)); err != nil || string(got) != "poster of the newer spelling" {
			t.Fatalf("poster of %s = %q, %v; want the newer spelling's", id, got, err)
		}
	}
}

// titleFoldMerges lists the title-fold merge activity entries, newest first.
func titleFoldMerges(t *testing.T, st *store.Store) []store.ActivityEntry {
	t.Helper()
	entries, err := st.ActivityEntries(context.Background(), store.ActivityQuery{
		Filter: store.LibraryFilter{AllLibraries: true, Revealed: true}, Limit: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ActivityEntry
	for _, entry := range entries {
		if entry.Action == store.ActivityEntityTitleFoldMerge {
			out = append(out, entry)
		}
	}
	return out
}

// TestTitleFoldRescanMergesVariantsThatKeepTheirSpelling pins CORE-2459's
// flat-layout path: three fold-equal variants each hold two Media, and the
// rescan after the repair hydrates every Media under its own unchanged
// spelling. The first Media's merge fails; the next Media of the same variant
// merges anyway, so the split does not survive behind completion markers, and
// the scan ends with one parent and one child holding all six Media.
func TestTitleFoldRescanMergesVariantsThatKeepTheirSpelling(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	type file struct {
		media    store.MediaID
		spelling string
	}
	var files []file
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	for i, spelling := range []string{"First Band", "First Band", "first.band", "first.band", "FIRST BAND", "FIRST BAND"} {
		name := string(rune('a' + i))
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: media.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": spelling},
		}); err != nil {
			t.Fatal(err)
		}
		files = append(files, file{media: media.ID, spelling: spelling})
	}
	entities := func(kind string) []store.Entity {
		t.Helper()
		out, err := st.EntitiesByKind(ctx, lib.ID, kind, all, store.EntityKindOptions{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := entities("artist"); len(got) != 3 {
		t.Fatalf("before rescan parents = %+v, want the three-way split", got)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore, failCopies: 1}
	adapter.foldStore = recorder
	hydrate := func(f file) error {
		return adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: f.media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "folder": f.spelling},
		})
	}
	if err := hydrate(files[0]); err == nil {
		t.Fatal("hydration with a failing merge succeeded")
	}
	for _, f := range files[1:] {
		if err := hydrate(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := hydrate(files[0]); err != nil {
		t.Fatalf("retry: %v", err)
	}
	artists, albums := entities("artist"), entities("album")
	if len(artists) != 1 || len(albums) != 1 {
		t.Fatalf("after rescan %d parents, %d children (%+v, %+v); want one each", len(artists), len(albums), artists, albums)
	}
	if members, err := st.MediaForEntity(ctx, albums[0].ID, all); err != nil || len(members) != len(files) {
		t.Fatalf("child members = %d, %v; want all %d Media", len(members), err, len(files))
	}
	if open, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField); err != nil || len(open) != 0 {
		t.Fatalf("unmarked after rescan = %v, %v; want none", open, err)
	}
}

// TestTitleFoldLeavesASharedChildVariantAlone pins CORE-2459's sibling merge
// to children of the survivor's parent alone: a newer fold-equal child also
// recorded under an unrelated parent is shared with that parent's item, so
// rehydrating the older child neither trashes it nor carries its other parent
// and Media over.
func TestTitleFoldLeavesASharedChildVariantAlone(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	lib, err := st.CreateLibrary(ctx, "Shelf", false, testHydrationPluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	all := store.LibraryFilter{AllLibraries: true, Revealed: true}
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	var media []store.MediaID
	for _, file := range []struct{ name, album, artist string }{{"one", "Shared", "First Band"}, {"two", "shared", "Other Band"}} {
		imported, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + file.name + ".flac", Title: file.name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + file.name,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: imported.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": file.album, "album-artist": file.artist},
		}); err != nil {
			t.Fatal(err)
		}
		media = append(media, imported.ID)
	}
	ids := map[string]store.EntityID{}
	for _, kind := range []string{"artist", "album"} {
		entities, err := st.EntitiesByKind(ctx, lib.ID, kind, all, store.EntityKindOptions{}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, entity := range entities {
			ids[entity.Title] = entity.ID
		}
	}
	older, shared, first, other := ids["Shared"], ids["shared"], ids["First Band"], ids["Other Band"]
	if older == "" || shared == "" || first == "" || other == "" {
		t.Fatalf("legacy entities = %v", ids)
	}
	// The newer child is also recorded under the first parent.
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{FromEntityID: shared, Name: "album-artist", ToEntityID: first}); err != nil {
		t.Fatal(err)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	if err := adapter.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
		MediaID: media[0], LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared", "folder": "First Band"},
	}); err != nil {
		t.Fatal(err)
	}
	if targets, _, err := st.EntityRelationTargets(ctx, lib.ID, older, "album-artist"); err != nil || len(targets) != 1 || targets[0] != first {
		t.Fatalf("older child scope = %v, %v; want %s alone", targets, err, first)
	}
	if members, err := st.MediaForEntity(ctx, shared, all); err != nil || len(members) != 1 || members[0].ID != media[1] {
		t.Fatalf("shared child members = %+v, %v; want its own Media", members, err)
	}
	if merges := titleFoldMerges(t, st); len(merges) != 0 {
		t.Fatalf("title-fold merge activity = %+v, want none", merges)
	}
}
