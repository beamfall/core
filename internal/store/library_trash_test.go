package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var revealedAll = LibraryFilter{Revealed: true, AllLibraries: true}

// trashFixtureLibrary creates a Library with one root folder holding one real
// file, imports that file as Media, and returns the Library, the Media id and
// the file's path.
func trashFixtureLibrary(t *testing.T, st *Store, title string) (Library, MediaID, string) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(path, []byte("source bytes of "+title), 0o600); err != nil {
		t.Fatal(err)
	}
	library, err := st.CreateLibraryWithRoots(ctx, title, false, "", "profile-default", []string{root}, "")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots: %v", err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID:          library.ID,
		Path:               path,
		Title:              title + " clip",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: "sha256:" + string(library.ID),
	})
	if err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	return library, media.ID, path
}

func trashedLibraryIDs(t *testing.T, st *Store, filter LibraryFilter) []LibraryID {
	t.Helper()
	trashed, err := st.TrashedLibraries(context.Background(), filter)
	if err != nil {
		t.Fatalf("TrashedLibraries: %v", err)
	}
	ids := make([]LibraryID, 0, len(trashed))
	for _, entry := range trashed {
		ids = append(ids, entry.Library.ID)
	}
	return ids
}

func TestTrashedLibrariesListsDeletedLibraryWithCountAndPurgeTime(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, mediaID, _ := trashFixtureLibrary(t, st, "Films")

	if got := trashedLibraryIDs(t, st, revealedAll); len(got) != 0 {
		t.Fatalf("trashed before delete = %v, want none", got)
	}
	// Media trashed on its own is not something a restore brings back.
	other, _, err := st.ImportMediaFile(ctx, ImportMediaFile{
		LibraryID: library.ID, Path: "/nowhere/other.mp4", Title: "Other", Container: "mp4", Codec: "h264",
		ContentFingerprint: "sha256:other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TrashMedia(ctx, other.ID, revealedAll); err != nil {
		t.Fatal(err)
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatalf("DeleteLibrary: %v", err)
	}
	trashed, err := st.TrashedLibraries(ctx, revealedAll)
	if err != nil {
		t.Fatalf("TrashedLibraries: %v", err)
	}
	if len(trashed) != 1 {
		t.Fatalf("trashed = %+v, want one entry", trashed)
	}
	entry := trashed[0]
	if entry.Library.ID != library.ID || entry.Library.Title != "Films" {
		t.Fatalf("entry library = %+v, want Films", entry.Library)
	}
	if entry.MediaCount != 1 {
		t.Fatalf("MediaCount = %d, want 1 (Media %s only)", entry.MediaCount, mediaID)
	}
	if entry.TrashedAt.Before(before) || entry.TrashedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("TrashedAt = %v, want about now", entry.TrashedAt)
	}
	purgeAfter, ok := entry.PurgeAfter()
	if !ok || !purgeAfter.Equal(entry.TrashedAt.Add(TrashRetention)) {
		t.Fatalf("PurgeAfter = %v, %v; want TrashedAt + TrashRetention", purgeAfter, ok)
	}
	if slices.Contains(libraryIDs(mustLibraries(t, st, revealedAll)), library.ID) {
		t.Fatal("deleted library still listed among live libraries")
	}
}

func TestTrashedLibrariesFollowsLibraryVisibility(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	restricted, err := st.CreateLibrary(ctx, "Private", true, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, restricted.ID); err != nil {
		t.Fatal(err)
	}
	if got := trashedLibraryIDs(t, st, LibraryFilter{AllLibraries: true}); len(got) != 0 {
		t.Fatalf("unrevealed scope sees trashed restricted library: %v", got)
	}
	if got := trashedLibraryIDs(t, st, LibraryFilter{AllowedIDs: []LibraryID{"library-local"}, Revealed: true}); len(got) != 0 {
		t.Fatalf("scope without the library sees it in trash: %v", got)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); !slices.Equal(got, []LibraryID{restricted.ID}) {
		t.Fatalf("revealed scope trashed = %v, want [%s]", got, restricted.ID)
	}
}

func TestTrashedLibraryCustodialIsNotPurgeable(t *testing.T) {
	entry := TrashedLibrary{Library: Library{Custodial: true}, TrashedAt: time.Now()}
	if entry.Purgeable() {
		t.Fatal("Custodial library reported purgeable")
	}
	if _, ok := entry.PurgeAfter(); ok {
		t.Fatal("Custodial library reported a purge time")
	}
}

func TestRestoreLibraryBringsBackMediaHistoryGrantsRootsAndSchedule(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, mediaID, _ := trashFixtureLibrary(t, st, "Films")
	insertRawProgressRow(t, st, "profile-default", mediaID)
	rootsBefore, err := st.LibraryRoots(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	schedule := "SELECT COUNT(*) FROM schedules WHERE id = 'scan:" + string(library.ID) + "'"
	grants := "SELECT COUNT(*) FROM profile_library WHERE library_id = '" + string(library.ID) + "'"
	if countCatalogRows(t, st, schedule) != 1 || countCatalogRows(t, st, grants) != 1 {
		t.Fatal("fixture library has no scan schedule or no profile grant")
	}

	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Media(ctx, mediaID, revealedAll); err == nil {
		t.Fatal("media of a deleted library is still readable")
	}
	if err := st.RestoreLibrary(ctx, library.ID); err != nil {
		t.Fatalf("RestoreLibrary: %v", err)
	}

	granted := LibraryFilter{AllowedIDs: []LibraryID{library.ID}}
	if !slices.Contains(libraryIDs(mustLibraries(t, st, granted)), library.ID) {
		t.Fatal("restored library not visible through its profile grant")
	}
	if _, err := st.Media(ctx, mediaID, granted); err != nil {
		t.Fatalf("media of restored library unreadable: %v", err)
	}
	if got := countEventRows(t, st, "playback_progress", mediaID); got != 1 {
		t.Fatalf("progress rows after restore = %d, want 1", got)
	}
	rootsAfter, err := st.LibraryRoots(ctx, library.ID)
	if err != nil || !slices.Equal(rootsAfter, rootsBefore) {
		t.Fatalf("roots after restore = %v, %v; want %v", rootsAfter, err, rootsBefore)
	}
	if countCatalogRows(t, st, schedule) != 1 || countCatalogRows(t, st, grants) != 1 {
		t.Fatal("scan schedule or profile grant lost across delete and restore")
	}
	if got := trashedLibraryIDs(t, st, revealedAll); len(got) != 0 {
		t.Fatalf("trashed after restore = %v, want none", got)
	}
}

func TestRestoreLibraryRefusesLiveAndUnknownLibraries(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	for _, id := range []LibraryID{"library-local", "no-such-library", ""} {
		err := st.RestoreLibrary(ctx, id)
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("RestoreLibrary(%q) = %v, want a validation error", id, err)
		}
	}
}

func TestPurgeLibraryRemovesRecordsAndLeavesFilesOnDisk(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, mediaID, path := trashFixtureLibrary(t, st, "Films")
	keep, keepMediaID, _ := trashFixtureLibrary(t, st, "Keep")
	sourceBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMediaExternalID(ctx, mediaID, "fixture", "purged"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryIcon(ctx, library.ID, "film", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	insertRawProgressRow(t, st, "profile-default", mediaID)
	insertRawProgressRow(t, st, "profile-default", keepMediaID)
	for _, statement := range []string{
		`INSERT INTO entity(id, library_id, kind, title) VALUES ('entity-purged', '` + string(library.ID) + `', 'fixture', 'Purged')`,
		`INSERT INTO entity(id, library_id, kind, title) VALUES ('entity-kept', '` + string(keep.ID) + `', 'fixture', 'Kept')`,
	} {
		if _, err := st.catalogW.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := st.eventsW.ExecContext(ctx,
		`INSERT INTO schedule_occurrence(schedule_id, due_ms, started_at_ms, completed_at_ms) VALUES (?, 1, 1, 1)`,
		scanScheduleID(library.ID)); err != nil {
		t.Fatal(err)
	}
	// The import itself records field provenance, which carries no foreign key.
	if countCatalogRows(t, st, `SELECT COUNT(*) FROM media_field_provenance WHERE media_id = '`+string(mediaID)+`'`) == 0 {
		t.Fatal("fixture media has no field provenance to purge")
	}
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}

	purge, err := st.PurgeLibrary(ctx, library.ID)
	if err != nil {
		t.Fatalf("PurgeLibrary: %v", err)
	}
	if purge.LibraryID != library.ID || purge.Title != "Films" || purge.Restricted {
		t.Fatalf("purge = %+v, want unrestricted Films", purge)
	}
	if !slices.Equal(purge.MediaIDs, []MediaID{mediaID}) || !slices.Equal(purge.EntityIDs, []EntityID{"entity-purged"}) {
		t.Fatalf("purge ids = media %v entities %v, want [%s] [entity-purged]", purge.MediaIDs, purge.EntityIDs, mediaID)
	}
	id := string(library.ID)
	for name, query := range map[string]string{
		"library":           `SELECT COUNT(*) FROM library WHERE id = '` + id + `'`,
		"media":             `SELECT COUNT(*) FROM media WHERE library_id = '` + id + `'`,
		"media files":       `SELECT COUNT(*) FROM media_file WHERE media_id = '` + string(mediaID) + `'`,
		"external ids":      `SELECT COUNT(*) FROM media_external_id WHERE media_id = '` + string(mediaID) + `'`,
		"field provenance":  `SELECT COUNT(*) FROM media_field_provenance WHERE media_id = '` + string(mediaID) + `'`,
		"entities":          `SELECT COUNT(*) FROM entity WHERE library_id = '` + id + `'`,
		"profile grants":    `SELECT COUNT(*) FROM profile_library WHERE library_id = '` + id + `'`,
		"root folders":      `SELECT COUNT(*) FROM library_roots WHERE library_id = '` + id + `'`,
		"scan schedule":     `SELECT COUNT(*) FROM schedules WHERE id = 'scan:` + id + `'`,
		"icon choice":       `SELECT COUNT(*) FROM server_config WHERE key = 'library.icon.` + id + `'`,
		"foreign-key fault": `SELECT COUNT(*) FROM pragma_foreign_key_check`,
	} {
		if got := countCatalogRows(t, st, query); got != 0 {
			t.Errorf("%s left after purge = %d, want 0", name, got)
		}
	}
	if got := countEventRows(t, st, "playback_progress", mediaID); got != 0 {
		t.Errorf("progress rows of purged media = %d, want 0", got)
	}
	var occurrences int
	if err := st.eventsR.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedule_occurrence WHERE schedule_id = ?`,
		scanScheduleID(library.ID)).Scan(&occurrences); err != nil || occurrences != 0 {
		t.Errorf("schedule occurrences left = %d, %v; want 0", occurrences, err)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); len(got) != 0 {
		t.Errorf("trashed after purge = %v, want none", got)
	}

	// The other Library is untouched.
	if _, err := st.Media(ctx, keepMediaID, revealedAll); err != nil {
		t.Errorf("media of the untouched library unreadable: %v", err)
	}
	if got := countEventRows(t, st, "playback_progress", keepMediaID); got != 1 {
		t.Errorf("progress rows of the untouched library = %d, want 1", got)
	}
	if got := countCatalogRows(t, st, `SELECT COUNT(*) FROM entity WHERE id = 'entity-kept'`); got != 1 {
		t.Errorf("entity of the untouched library = %d, want 1", got)
	}

	// The purge deletes records, never the owner's files.
	sourceAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("source file gone after purge: %v", err)
	}
	if !bytes.Equal(sourceAfter, sourceBefore) {
		t.Fatal("source file changed by purge")
	}
}

func TestPurgeLibraryRefusesLiveUnknownAndCustodialLibraries(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, mediaID, _ := trashFixtureLibrary(t, st, "Films")

	for _, id := range []LibraryID{library.ID, "no-such-library", ""} {
		_, err := st.PurgeLibrary(ctx, id)
		var validation ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("PurgeLibrary(%q) = %v, want a validation error", id, err)
		}
	}
	if _, err := st.Media(ctx, mediaID, revealedAll); err != nil {
		t.Fatalf("refused purge of a live library removed its media: %v", err)
	}

	if err := st.SetLibraryBacking(ctx, library.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeLibrary(ctx, library.ID); err == nil {
		t.Fatal("PurgeLibrary removed a Custodial library, which holds the only copy of its media")
	}
	if err := st.RestoreLibrary(ctx, library.ID); err != nil {
		t.Fatalf("Custodial library not restorable after refused purge: %v", err)
	}
	if _, err := st.Media(ctx, mediaID, revealedAll); err != nil {
		t.Fatalf("Custodial media lost: %v", err)
	}
}

func TestPurgedLibraryIDIsNeverReissued(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, _, _ := trashFixtureLibrary(t, st, "Films")
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeLibrary(ctx, library.ID); err != nil {
		t.Fatalf("PurgeLibrary: %v", err)
	}

	// Activity and history still name the purged id, so a new Library with the
	// same title must not inherit it.
	again, err := st.CreateLibraryWithRoots(ctx, "Films", false, "", "profile-default", []string{t.TempDir()}, "")
	if err != nil {
		t.Fatalf("CreateLibraryWithRoots: %v", err)
	}
	if again.ID == library.ID {
		t.Fatalf("new Library reused purged id %q", library.ID)
	}
}

func TestSweepPurgeTrashedLibrariesHonoursRetentionAndCustodial(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	films, _, _ := trashFixtureLibrary(t, st, "Films")
	backup, _, _ := trashFixtureLibrary(t, st, "Backup")
	if err := st.SetLibraryBacking(ctx, backup.ID, true, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []LibraryID{films.ID, backup.ID} {
		if err := st.DeleteLibrary(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	// Inside the retention window nothing goes.
	purged, skipped, err := st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(-TrashRetention))
	if err != nil || skipped || len(purged) != 0 {
		t.Fatalf("sweep inside retention = %v, skipped %v, err %v; want nothing", purged, skipped, err)
	}

	purged, skipped, err = st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || skipped {
		t.Fatalf("sweep past retention: skipped %v, err %v", skipped, err)
	}
	if len(purged) != 1 || purged[0].LibraryID != films.ID {
		t.Fatalf("purged = %+v, want only Films", purged)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); !slices.Equal(got, []LibraryID{backup.ID}) {
		t.Fatalf("trashed after sweep = %v, want only the Custodial library", got)
	}
	entries, err := st.ActivityEntries(ctx, ActivityQuery{Filter: revealedAll})
	if err != nil {
		t.Fatal(err)
	}
	audited := slices.ContainsFunc(entries, func(entry ActivityEntry) bool {
		return entry.Action == ActivityLibraryPurge && entry.TargetType == "library" && entry.TargetID == string(films.ID)
	})
	if !audited {
		t.Fatalf("no readable library.purge activity record for %s in %+v", films.ID, entries)
	}
}

func TestSweepPurgeTrashedLibrariesYieldsWhileTrashDisabled(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, TrashDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	library, _, _ := trashFixtureLibrary(t, st, "Films")
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}

	purged, skipped, err := st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour))
	if !skipped || !errors.Is(err, ErrTrashDisabledConfirmationRequired) || len(purged) != 0 {
		t.Fatalf("sweep with trash disabled = %v, skipped %v, err %v; want a confirmation-required skip", purged, skipped, err)
	}
	// A human-confirmed purge stays legal.
	if _, err := st.PurgeLibrary(ctx, library.ID); err != nil {
		t.Fatalf("confirmed PurgeLibrary with trash disabled: %v", err)
	}
}

func TestPurgeLibraryRefusesEncryptedLibraryAndSweepSkipsIt(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, _, _ := trashFixtureLibrary(t, st, "Vault")
	if err := st.SetLibraryBacking(ctx, library.ID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	var validation ValidationError
	if _, err := st.PurgeLibrary(ctx, library.ID); !errors.As(err, &validation) {
		t.Fatalf("PurgeLibrary(encrypted) = %v, want a validation error", err)
	}
	purged, skipped, err := st.SweepPurgeTrashedLibrariesBefore(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || skipped || len(purged) != 0 {
		t.Fatalf("sweep = %v, skipped %v, err %v; want the encrypted library left alone", purged, skipped, err)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); !slices.Equal(got, []LibraryID{library.ID}) {
		t.Fatalf("trashed after sweep = %v, want the encrypted library", got)
	}
}

func TestPurgeLibraryLockedHonoursTrashedBeforeBound(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	library, _, _ := trashFixtureLibrary(t, st, "Films")
	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	// A cutoff older than the trash time models a Library restored and deleted
	// again after the sweep chose its candidates: the purge transaction itself
	// must refuse, not just the candidate query.
	st.mu.RLock()
	_, err := st.purgeLibraryLocked(ctx, library.ID, 1)
	st.mu.RUnlock()
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("purgeLibraryLocked with an older cutoff = %v, want a validation error", err)
	}
	if got := trashedLibraryIDs(t, st, revealedAll); !slices.Equal(got, []LibraryID{library.ID}) {
		t.Fatalf("trashed = %v, want the library kept", got)
	}
}
