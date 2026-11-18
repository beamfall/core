package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

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

// recordingFoldStore fails the first failCopies item-state copies and records
// every merge batch, so a test can retry a failed merge and undo a good one.
type recordingFoldStore struct {
	hydrationFoldStore
	failCopies int
	batches    []store.BulkOpBatch
}

func (r *recordingFoldStore) CopyEntityItemState(ctx context.Context, from, to store.EntityID) error {
	if r.failCopies > 0 {
		r.failCopies--
		return errors.New("copy failed")
	}
	return r.hydrationFoldStore.CopyEntityItemState(ctx, from, to)
}

func (r *recordingFoldStore) MergeEntities(ctx context.Context, winner, loser store.EntityID, filter store.LibraryFilter, now time.Time) (store.BulkOpBatch, error) {
	batch, err := r.hydrationFoldStore.MergeEntities(ctx, winner, loser, filter, now)
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
	if err := adapter.HydrateMetadata(ctx, input); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := st.Entity(ctx, oldHolder, all); err == nil {
		t.Fatalf("old holder %s still live after the retried merge", oldHolder)
	}
	if len(recorder.batches) != 2 {
		t.Fatalf("merge batches = %d, want the parent and the child", len(recorder.batches))
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
