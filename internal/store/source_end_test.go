package store

import (
	"context"
	"os"
	"path/filepath"
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
