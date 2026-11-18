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
// ("First Band", "first.band") holds two parents and two scoped children. The
// one-time repair reopens their Media, the next scan hydrates them under the
// one settled spelling, and the old Entities merge into the new ones: one
// parent and one child hold every Media, the loser is trashed (recoverable),
// and a rating and a watchlist entry recorded on the losers carry over.
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
	for name, artist := range map[string]string{"one": "First Band", "two": "first.band", "three": "FIRST BAND"} {
		media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: lib.ID, Path: "/shelf/" + name + ".flac", Title: name, Container: "flac", Codec: "flac",
			Class: store.MediaClassAudio, ContentFingerprint: "fp-" + name,
		})
		if err != nil {
			t.Fatal(err)
		}
		spelling[media.ID] = artist
	}

	// The split: every spelling hydrated into its own parent and child.
	legacy := newTestAdapterWithMapper(st, scopedAlbumMapper{})
	for media, artist := range spelling {
		if err := legacy.HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
			MediaID: media, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
			Metadata: map[string]string{"album": "Shared Collection", "album-artist": artist},
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaFields(ctx, media, []store.MediaField{{Name: testHydrationMarkerField, Type: store.EntityFieldFlag, Flag: true}}); err != nil {
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
	var loserArtist, loserAlbum store.EntityID
	for _, artist := range artists {
		if artist.Title == "first.band" {
			loserArtist = artist.ID
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
	if len(artists) != 1 || artists[0].Title != "First Band" {
		t.Fatalf("after rescan parents = %+v, want one \"First Band\"", artists)
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
// failure and undo paths for a holder with a single Media: a failed merge fails
// the hydration with the Media still on its previous holder, the next
// hydration merges, and undoing the merges gives the old holder its Media back.
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
	media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: lib.ID, Path: "/shelf/one.flac", Title: "one", Container: "flac", Codec: "flac",
		Class: store.MediaClassAudio, ContentFingerprint: "fp-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := newTestAdapterWithMapper(st, scopedAlbumMapper{}).HydrateMetadata(ctx, scanpkg.MetadataHydrationInput{
		MediaID: media.ID, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "album-artist": "first.band"},
	}); err != nil {
		t.Fatal(err)
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
	if open, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField); err != nil || len(open) != 1 {
		t.Fatalf("after failed merge unmarked Media = %v, %v; want it left for the next scan", open, err)
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
	for i := len(recorder.batches) - 1; i >= 0; i-- {
		if _, err := st.UndoBatch(ctx, recorder.batches[i].ID, all, time.Now().UTC()); err != nil {
			t.Fatalf("undo merge: %v", err)
		}
	}
	if members, err := st.MediaForEntity(ctx, oldHolder, all); err != nil || len(members) != 1 || members[0].ID != media.ID {
		t.Fatalf("after undo the old holder has %v, %v; want the Media back", members, err)
	}
}

// TestTitleFoldNewSpellingAbsorbsTheOnlyExistingVariant pins CORE-2459's
// upgrade path for a container no older scan split: every file hydrated under
// one file-name spelling, so the one-time repair has nothing to reopen. The
// first new file named by the folder's spelling creates its container, and the
// existing fold-equal parent and child merge into it with their Media and item
// state instead of staying beside it as twins. A failed merge is retried by
// the next hydration, and undoing the merges keeps the new Media on its new
// container.
func TestTitleFoldNewSpellingAbsorbsTheOnlyExistingVariant(t *testing.T) {
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
	old := entities("artist")
	if len(old) != 1 {
		t.Fatalf("legacy parents = %+v, want one", old)
	}
	const profile = store.ProfileID("profile-default")
	if err := st.SetItemRating(ctx, profile, store.ItemTarget{EntityID: old[0].ID}, 7, all); err != nil {
		t.Fatal(err)
	}

	oldAlbums := entities("album")
	if len(oldAlbums) != 1 {
		t.Fatalf("legacy children = %+v, want one", oldAlbums)
	}

	adapter := newTestAdapterWithMapper(st, foldedAlbumMapper{})
	recorder := &recordingFoldStore{hydrationFoldStore: adapter.foldStore, failCopies: 1}
	adapter.foldStore = recorder
	three := importMedia("three")
	input := scanpkg.MetadataHydrationInput{
		MediaID: three, LibraryID: lib.ID, Class: store.MediaClassAudio, Path: "/x.bin",
		Metadata: map[string]string{"album": "Shared Collection", "folder": "First Band"},
	}
	if err := adapter.HydrateMetadata(ctx, input); err == nil {
		t.Fatal("hydration with a failing merge succeeded")
	}
	if open, err := st.MediaLackingFieldInLibrary(ctx, lib.ID, store.MediaClassAudio, testHydrationMarkerField); err != nil {
		t.Fatal(err)
	} else if _, ok := open[three]; !ok {
		t.Fatalf("after failed merge unmarked Media = %v; want %s left for the next scan", open, three)
	}
	if err := adapter.HydrateMetadata(ctx, input); err != nil {
		t.Fatalf("retry: %v", err)
	}
	artists, albums := entities("artist"), entities("album")
	if len(artists) != 1 || artists[0].Title != "First Band" {
		t.Fatalf("parents = %+v, want one \"First Band\"", artists)
	}
	if len(albums) != 1 {
		t.Fatalf("children = %+v, want one", albums)
	}
	if members, err := st.MediaForEntity(ctx, albums[0].ID, all); err != nil || len(members) != 3 {
		t.Fatalf("child members = %d, %v; want all three Media", len(members), err)
	}
	if targets, _, err := st.EntityRelationTargets(ctx, lib.ID, albums[0].ID, "album-artist"); err != nil || len(targets) != 1 || targets[0] != artists[0].ID {
		t.Fatalf("child scope = %v, %v; want %s", targets, err, artists[0].ID)
	}
	if state, err := st.ItemStateFor(ctx, profile, store.ItemTarget{EntityID: artists[0].ID}, all); err != nil || state.Rating != 7 {
		t.Fatalf("parent state = %+v, %v; want the old rating 7", state, err)
	}
	if len(recorder.batches) != 2 {
		t.Fatalf("merge batches = %d, want the parent and the child", len(recorder.batches))
	}
	for i := len(recorder.batches) - 1; i >= 0; i-- {
		if _, err := st.UndoBatch(ctx, recorder.batches[i].ID, all, time.Now().UTC()); err != nil {
			t.Fatalf("undo merge: %v", err)
		}
	}
	if members, err := st.MediaForEntity(ctx, oldAlbums[0].ID, all); err != nil || len(members) != 2 {
		t.Fatalf("after undo the old child has %d members, %v; want its two Media back", len(members), err)
	}
	if members, err := st.MediaForEntity(ctx, albums[0].ID, all); err != nil || len(members) != 1 || members[0].ID != three {
		t.Fatalf("after undo the new child has %v, %v; want the new Media kept", members, err)
	}
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
