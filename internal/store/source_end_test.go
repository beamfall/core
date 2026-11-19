package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceEndPersistsOnlyForTheObservedGeneration(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(t.TempDir(), "source.mkv")
	if err := os.WriteFile(path, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{LibraryID: "library-local", Path: path, Title: "Source", Container: "mkv", Codec: "h264", Class: MediaClassVideo, ContentFingerprint: "source-generation"})
	if err != nil {
		t.Fatal(err)
	}
	files, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v", files, err)
	}
	observed, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	generation := SourceFileGeneration(observed)
	if generation == "" {
		t.Skip("platform has no stable file identity")
	}
	if err := st.RecordSourceEnd(ctx, media.ID, path, "", generation, 152); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		got, err := st.MediaFileSourceIncomplete(ctx, files[0])
		if err != nil || got != want {
			t.Fatalf("incomplete = %v, %v; want %v", got, err, want)
		}
	}
	check(true)
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, []byte("updated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, observed.ModTime(), observed.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	check(false) // Same size and mtime, different inode.
	if err := st.RecordSourceEnd(ctx, media.ID, path, "", generation, 120); err != nil {
		t.Fatal(err)
	}
	check(false) // Late completion from the old encoder cannot poison it.
	current, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordSourceEnd(ctx, media.ID, path, "", SourceFileGeneration(current), 153); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordSourceEnd(ctx, media.ID, path, "", generation, 100); err != nil {
		t.Fatal(err)
	}
	check(true) // Old and new observations cannot overwrite each other.
}

func TestEncryptedSourceEndLivesInOwningCatalogAcrossSeal(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "incomplete-source")
	defer st.Close()
	filter := LibraryFilter{AllLibraries: true, Revealed: true}
	files, err := st.MediaFilesForMedia(ctx, media.ID, filter)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v", files, err)
	}
	file := files[0]
	if err := st.RecordSourceEnd(ctx, media.ID, "", file.ContentFingerprint, EncryptedSourceGeneration(file.ContentFingerprint), 12); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT count(*) FROM source_end_observation`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("encrypted observations leaked into shared catalog: count=%d, err=%v", count, err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	incomplete, err := st.MediaFileSourceIncomplete(ctx, file)
	if err != nil || !incomplete {
		t.Fatalf("encrypted observation lost after catalog reopen: %v, %v", incomplete, err)
	}
}

func TestContinueWatchingFiltersSourceEndsBeforePagination(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var healthy []MediaID
	for _, name := range []string{"first", "second", "incomplete"} {
		path := filepath.Join(t.TempDir(), name+".mkv")
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		media, _, err := st.ImportMediaFile(ctx, ImportMediaFile{LibraryID: "library-local", Path: path, Title: name, Container: "mkv", Codec: "h264", Class: MediaClassVideo, ContentFingerprint: "pagination-" + name})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertPlaybackProgress(ctx, PlaybackProgress{ProfileID: "profile-default", MediaID: media.ID, PositionSeconds: 163, DurationSeconds: 6906}, false, ResumePolicy{Class: MediaClassVideo}); err != nil {
			t.Fatal(err)
		}
		if name == "incomplete" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.RecordSourceEnd(ctx, media.ID, path, "", SourceFileGeneration(info), 152); err != nil {
				t.Fatal(err)
			}
		} else {
			healthy = append(healthy, media.ID)
		}
	}
	seen := map[MediaID]bool{}
	for offset := range 2 {
		items, err := st.SourceItems(ctx, LibraryFilter{AllLibraries: true}, SourceQuery{Kind: "continue-watching", ProfileID: "profile-default", Limit: 1, Offset: offset})
		if err != nil || len(items) != 1 {
			t.Fatalf("page %d = %v, %v", offset, items, err)
		}
		seen[items[0].ID] = true
	}
	for _, id := range healthy {
		if !seen[id] {
			t.Fatalf("healthy item %s skipped by paging: %v", id, seen)
		}
	}
}

// Hold EOF detection open while sealing queues behind it, then fail the actual
// INSERT. Sealing must retry and refuse to discard the pending observation.
func TestSourceEndFailureInterleavesWithSeal(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "source-end-seal-race")
	defer func() { _ = st.Close() }()
	files, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v", files, err)
	}
	file := files[0]
	generation := EncryptedSourceGeneration(file.ContentFingerprint)
	// Prove the fixture and normal write path before injecting failure.
	if err := st.RecordSourceEnd(ctx, media.ID, "", file.ContentFingerprint, generation, 100); err != nil {
		t.Fatal(err)
	}
	db := st.encryptedDBs[media.LibraryID].writer
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_source_end BEFORE INSERT ON source_end_observation BEGIN SELECT RAISE(FAIL, 'injected source-end write failure'); END`); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	recorded := make(chan error, 1)
	go func() {
		recorded <- st.ObserveSourceEnd(ctx, media.ID, "", file.ContentFingerprint, generation, func() (float64, bool) {
			close(entered)
			<-release
			return 12, true
		})
	}()
	<-entered
	// The callback runs under the exact lock sealing needs, not a playback-only
	// lock. This assertion fails immediately if that coordination regresses.
	unlocked := st.mu.TryLock()
	if unlocked {
		st.mu.Unlock()
	}
	started := make(chan struct{})
	sealed := make(chan error, 1)
	go func() {
		close(started)
		sealed <- st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{})
	}()
	<-started
	close(release)
	writeErr, sealErr := <-recorded, <-sealed
	if unlocked {
		t.Fatal("EOF detection did not hold the seal lock")
	}
	for name, err := range map[string]error{"write": writeErr, "seal retry": sealErr} {
		if err == nil || !strings.Contains(err.Error(), "injected source-end write failure") {
			t.Fatalf("%s did not reach injected INSERT: %v", name, err)
		}
	}
	if !st.EncryptedLibraryDBRegistered(media.LibraryID) || len(st.pendingSourceEnds) != 1 {
		t.Fatal("failed seal discarded catalog or pending observation")
	}
	if err := st.Close(); err == nil {
		t.Fatal("Close discarded a failed pending write")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_source_end`); err != nil {
		t.Fatal(err)
	}
	// No second encoder notification: sealing itself must retry the failed 12s
	// bound, not resurrect the older successful 100s bound.
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	if len(st.pendingSourceEnds) != 0 {
		t.Fatal("successful retry left pending work")
	}
	dataDir := st.dataDir
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, dataDir, Options{EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	var end float64
	if err := st.encryptedDBs[media.LibraryID].reader.QueryRowContext(ctx, `SELECT end_seconds FROM source_end_observation WHERE content_fingerprint = ?`, file.ContentFingerprint).Scan(&end); err != nil || end != 12 {
		t.Fatalf("restarted bound = %v, %v", end, err)
	}
	if incomplete, err := st.MediaFileSourceIncomplete(ctx, file); err != nil || !incomplete {
		t.Fatalf("restarted incomplete = %v, %v", incomplete, err)
	}
	var count int
	if err := st.catalogR.QueryRowContext(ctx, `SELECT count(*) FROM source_end_observation`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("encrypted observation leaked: %d, %v", count, err)
	}
}

func TestSourceEndAfterSealDoesNotInspectRevokedOutput(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "source-end-seal-first")
	defer st.Close()
	if err := st.SealEncryptedLibraryDB(ctx, media.LibraryID, copyEncryptedLibraryDBCipher{}); err != nil {
		t.Fatal(err)
	}
	called := false
	err := st.ObserveSourceEnd(ctx, media.ID, "", "fingerprint", "generation", func() (float64, bool) { called = true; return 12, true })
	if err == nil || called {
		t.Fatalf("sealed source observation = called %v, err %v", called, err)
	}
}

func TestSourceEndReadRetriesFailedWrite(t *testing.T) {
	ctx := context.Background()
	st, media := seededEncryptedMedia(t, "source-end-read-retry")
	defer st.Close()
	files, err := st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v, %v", files, err)
	}
	file := files[0]
	if incomplete, err := st.MediaFileSourceIncomplete(ctx, file); err != nil || incomplete {
		t.Fatalf("baseline incomplete = %v, %v", incomplete, err)
	}
	db := st.encryptedDBs[media.LibraryID].writer
	alternate, _, err := st.ImportMediaFile(ctx, encryptedImport(media.LibraryID, "sha256:healthy-alternate", "/enc/healthy-alternate.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE media_file SET media_id = ?, version_label = 'alternate' WHERE media_id = ?`, media.ID, alternate.ID); err != nil {
		t.Fatal(err)
	}
	files, err = st.MediaFilesForMedia(ctx, media.ID, LibraryFilter{AllLibraries: true, Revealed: true})
	if err != nil || len(files) != 2 {
		t.Fatalf("Version files = %v, %v", files, err)
	}
	var healthy MediaFile
	for _, candidate := range files {
		if candidate.VersionLabel == "alternate" {
			healthy = candidate
		}
	}
	if healthy.ContentFingerprint == "" {
		t.Fatal("missing healthy Version fixture")
	}

	// Ensure both the INSERT and the read fail through the intended trigger.
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_source_end BEFORE INSERT ON source_end_observation BEGIN SELECT RAISE(FAIL, 'injected source-end write failure'); END`); err != nil {
		t.Fatal(err)
	}
	err = st.RecordSourceEnd(ctx, media.ID, "", file.ContentFingerprint, EncryptedSourceGeneration(file.ContentFingerprint), 12)
	if err == nil || !strings.Contains(err.Error(), "injected source-end write failure") {
		t.Fatalf("write did not reach trigger: %v", err)
	}
	if _, err := st.MediaFileSourceIncomplete(ctx, file); err == nil || !strings.Contains(err.Error(), "injected source-end write failure") {
		t.Fatalf("read did not retry pending write: %v", err)
	}
	if incomplete, err := st.MediaFileSourceIncomplete(ctx, healthy); err != nil || incomplete {
		t.Fatalf("unrelated Version blocked by pending write: %v, %v", incomplete, err)
	}
	blocked, err := st.incompleteMediaIDsSQL(ctx, []MediaID{media.ID})
	if err != nil || !blocked[media.ID] {
		t.Fatalf("pending observation missing from shelf filter: %v, %v", blocked, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER fail_source_end`); err != nil {
		t.Fatal(err)
	}
	if incomplete, err := st.MediaFileSourceIncomplete(ctx, file); err != nil || !incomplete {
		t.Fatalf("retry incomplete = %v, %v", incomplete, err)
	}
	if len(st.pendingSourceEnds) != 0 {
		t.Fatal("successful read did not flush pending write")
	}
}
