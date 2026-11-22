package store

import (
	"errors"
	"slices"
	"testing"
	"time"
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
	if updated, err := st.SetProbedMediaDuration(ctx, media.LibraryID, media.ID, file.ContentFingerprint, file.Path, 7152); updated || err != nil {
		t.Fatalf("write-back = %v, %v; want false, nil", updated, err)
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
		if updated, err := st.SetProbedMediaDuration(ctx, libraryID, id, file.ContentFingerprint, file.Path, 7152); updated != (want > 0) || err != nil {
			t.Fatalf("write-back for %s = %v, %v; want %v, nil", file.Path, updated, err, want > 0)
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
	setDuration := func() (bool, error) {
		return st.SetProbedMediaDuration(ctx, libraryID, media.ID, file.ContentFingerprint, file.Path, 7152)
	}
	if updated, err := setDuration(); updated || !errors.Is(err, ErrEncryptedLibraryDBDisabled) {
		t.Fatalf("sealed write = %v, %v; want false, %v", updated, err, ErrEncryptedLibraryDBDisabled)
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
	if updated, err := setDuration(); !updated || err != nil {
		t.Fatalf("unsealed write = %v, %v; want true, nil", updated, err)
	}
	if err := st.SealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}
	readDuration(7152)
}

// TestImportMediaFilePartsResettingMarkersClearsOnRegroup covers CORE-3093: a
// change in Part membership drops the named derived Markers in the import
// transaction, so a scan that stops before rewriting them leaves an empty set
// (rebuilt next scan), never a stale one. Other methods and an unchanged
// membership keep their Markers.
func TestImportMediaFilePartsResettingMarkersClearsOnRegroup(t *testing.T) {
	st := openSeeded(t)
	ctx := t.Context()
	const libraryID = "library-local"
	reset := []string{"part-boundary", "chapter-import"}
	one := ImportMediaFilePart{File: versionImport(libraryID, "/discs/one.m4b", "Book"), SourceOrdinal: 1}
	two := ImportMediaFilePart{File: versionImport(libraryID, "/discs/two.m4b", "Book"), SourceOrdinal: 2}
	media, _, err := st.ImportMediaFilePartsResettingMarkers(ctx, []ImportMediaFilePart{one}, reset)
	if err != nil {
		t.Fatal(err)
	}
	filter := LibraryFilter{AllLibraries: true}
	seed := func() {
		t.Helper()
		end := 10.0
		for _, method := range []string{"part-boundary", "chapter-import", "silence-detect"} {
			if err := st.ReplaceDetectedMarkers(ctx, media.ID, method, []Marker{{Title: method, StartSeconds: 0, EndSeconds: &end}}, filter, time.Now()); err != nil {
				t.Fatalf("seed %s: %v", method, err)
			}
		}
	}
	methods := func() []string {
		t.Helper()
		markers, err := st.MarkersForMedia(ctx, media.ID, filter)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(markers))
		for _, marker := range markers {
			out = append(out, marker.Method)
		}
		slices.Sort(out)
		return out
	}
	seed()
	if _, _, err := st.ImportMediaFilePartsResettingMarkers(ctx, []ImportMediaFilePart{one, two}, reset); err != nil {
		t.Fatal(err)
	}
	if got := methods(); !slices.Equal(got, []string{"silence-detect"}) {
		t.Fatalf("markers after regroup = %v, want [silence-detect]", got)
	}
	seed()
	if _, _, err := st.ImportMediaFilePartsResettingMarkers(ctx, []ImportMediaFilePart{one, two}, reset); err != nil {
		t.Fatal(err)
	}
	if got := methods(); !slices.Equal(got, []string{"chapter-import", "part-boundary", "silence-detect"}) {
		t.Fatalf("markers after unchanged rescan = %v, want all three", got)
	}

	// A Part moving to another Media regroups its source Media as well.
	three := ImportMediaFilePart{File: versionImport(libraryID, "/discs/three.m4b", "Other"), SourceOrdinal: 1}
	other, _, err := st.ImportMediaFilePartsResettingMarkers(ctx, []ImportMediaFilePart{three}, reset)
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == media.ID {
		t.Fatalf("separate import landed on the same Media %s", other.ID)
	}
	if _, _, err := st.ImportMediaFilePartsResettingMarkers(ctx, []ImportMediaFilePart{three, two}, reset); err != nil {
		t.Fatal(err)
	}
	if got := methods(); !slices.Equal(got, []string{"silence-detect"}) {
		t.Fatalf("source markers after losing a Part = %v, want [silence-detect]", got)
	}
}
