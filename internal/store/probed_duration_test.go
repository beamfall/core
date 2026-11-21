package store

import (
	"errors"
	"testing"
)

func TestSetProbedMediaDurationKeepsScannedRuntime(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	imported := versionImport("library-local", "/scan/source.mkv", "Scanned source")
	media, file, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	imported.DurationSecondsHint = 120
	if _, _, err := st.ImportMediaFile(ctx, imported); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProbedMediaDuration(ctx, media.LibraryID, media.ID, file.ContentFingerprint, file.Path, 7152); err != nil {
		t.Fatal(err)
	}
	got, err := st.Media(ctx, media.ID, LibraryFilter{AllLibraries: true})
	if err != nil || got.DurationSeconds != 120 {
		t.Fatalf("duration after write-back = %d, %v; want the scanned 120", got.DurationSeconds, err)
	}
}

func TestSetProbedMediaDurationSkipsAlternateVersionsAndParts(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const libraryID = "library-local"
	media, primary, err := st.ImportMediaFile(ctx, versionImport(libraryID, "/versions/primary.mkv", "Versions"))
	if err != nil {
		t.Fatal(err)
	}
	alternate, err := st.ImportMediaFileVersion(ctx, MediaVersionImport{
		MediaID: media.ID,
		File:    versionImport(libraryID, "/versions/alternate.mkv", "Versions"),
		Label:   "Alternate",
	})
	if err != nil {
		t.Fatal(err)
	}
	multipart, parts, err := st.ImportMediaFileParts(ctx, []ImportMediaFilePart{
		{File: versionImport(libraryID, "/parts/one.mkv", "Multipart"), SourceOrdinal: 1},
		{File: versionImport(libraryID, "/parts/two.mkv", "Multipart"), SourceOrdinal: 2},
	})
	if err != nil || len(parts) != 2 {
		t.Fatalf("multipart import returned %d Parts, error %v", len(parts), err)
	}
	check := func(id MediaID, file MediaFile, want int) {
		t.Helper()
		if err := st.SetProbedMediaDuration(ctx, libraryID, id, file.ContentFingerprint, file.Path, 7152); err != nil {
			t.Fatal(err)
		}
		got, err := st.Media(ctx, id, LibraryFilter{AllLibraries: true})
		if err != nil || got.DurationSeconds != want {
			t.Fatalf("duration after probing %s = %d, %v; want %d", file.Path, got.DurationSeconds, err, want)
		}
	}
	check(media.ID, alternate, 0)
	check(media.ID, primary, 7152)
	for _, part := range parts {
		check(multipart.ID, part, 0)
	}
}

func TestSetProbedMediaDurationUsesUnlockedEncryptedCatalog(t *testing.T) {
	ctx := t.Context()
	st, err := Open(ctx, migratedDataDir(t), Options{SeedDevelopmentData: true, EnableEncryptedLibraryDBLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	libraryID := LibraryID("library-local")
	cipher := copyEncryptedLibraryDBCipher{}
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	imported := encryptedImport(libraryID, "sha256:runtime", "/encrypted/source.mkv")
	imported.DurationSecondsHint = 0
	media, file, err := st.ImportMediaFile(ctx, imported)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	setDuration := func() error {
		return st.SetProbedMediaDuration(ctx, libraryID, media.ID, file.ContentFingerprint, file.Path, 7152)
	}
	if err := setDuration(); !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed write error = %v, want %v", err, ErrEncryptedLibraryDBDisabled)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	readDuration := func(want int) {
		t.Helper()
		got, err := st.Media(ctx, media.ID, LibraryFilter{AllLibraries: true})
		if err != nil || got.DurationSeconds != want {
			t.Fatalf("protected duration = %d, %v; want %d", got.DurationSeconds, err, want)
		}
	}
	readDuration(0)
	if err := setDuration(); err != nil {
		t.Fatal(err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	readDuration(7152)
}
