package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// liveFileAt returns the live media_file row cataloged at path in library-local.
func liveFileAt(t *testing.T, st *store.Store, path string) store.MediaFile {
	t.Helper()
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatalf("MediaFilesForLibrary: %v", err)
	}
	for _, file := range files {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("no live media file at %q in %#v", path, files)
	return store.MediaFile{}
}

func contentFingerprint(t *testing.T, path string) string {
	t.Helper()
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatalf("HashMediaFileContent(%q): %v", path, err)
	}
	return fingerprint
}

func importFromOutside(t *testing.T, st *store.Store, root string, filter store.LibraryFilter, name, payload string) store.ManagedPath {
	t.Helper()
	token, err := st.RegisterImportSource(writeOutOfLibrarySource(t, name, payload))
	if err != nil {
		t.Fatalf("RegisterImportSource: %v", err)
	}
	imported, err := st.ImportManagedFile(context.Background(), "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
	if err != nil {
		t.Fatalf("ImportManagedFile(%s): %v", name, err)
	}
	return imported
}

// TestManagedImportIdentityFollowsContent proves managed import fingerprints the
// admitted bytes, not the path: the same bytes imported under two names resolve
// to one Media, and new bytes at a reused name get a new identity.
func TestManagedImportIdentityFollowsContent(t *testing.T) {
	t.Run("same bytes at a second path are refused", func(t *testing.T) {
		st, root, filter := importStageHarness(t)
		first := importFromOutside(t, st, root, filter, "first.mkv", "identical-bytes")
		firstFile := liveFileAt(t, st, first.Path)
		if want := contentFingerprint(t, first.Path); firstFile.ContentFingerprint != want {
			t.Fatalf("first fingerprint = %q, want content hash %q", firstFile.ContentFingerprint, want)
		}
		firstMedia, err := st.Media(context.Background(), firstFile.MediaID, filter)
		if err != nil {
			t.Fatalf("Media: %v", err)
		}

		token, err := st.RegisterImportSource(writeOutOfLibrarySource(t, "second.mkv", "identical-bytes"))
		if err != nil {
			t.Fatalf("RegisterImportSource: %v", err)
		}
		_, err = st.ImportManagedFile(context.Background(), "library-local", token, root, "mkv", "h264", store.MediaClassVideo, filter)
		var validation store.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("duplicate import err = %v, want ValidationError", err)
		}
		if _, statErr := os.Lstat(filepath.Join(root, "second.mkv")); !os.IsNotExist(statErr) {
			t.Fatalf("refused import left a file at the second path: %v", statErr)
		}
		after := liveFileAt(t, st, first.Path)
		if after.MediaID != firstFile.MediaID || after.ContentFingerprint != firstFile.ContentFingerprint {
			t.Fatalf("first row after refused import = %#v, want %#v", after, firstFile)
		}
		media, err := st.Media(context.Background(), firstFile.MediaID, filter)
		if err != nil {
			t.Fatalf("Media after refused import: %v", err)
		}
		if media.LibraryID != firstMedia.LibraryID {
			t.Fatalf("first media library = %q, want %q", media.LibraryID, firstMedia.LibraryID)
		}
	})

	t.Run("changed bytes at a reused path get new identity", func(t *testing.T) {
		st, root, filter := importStageHarness(t)
		original := importFromOutside(t, st, root, filter, "reused.mkv", "original-bytes")
		originalFile := liveFileAt(t, st, original.Path)
		if _, err := st.DeleteManagedPath(context.Background(), "library-local", original.Path, filter, time.Unix(1700000000, 0)); err != nil {
			t.Fatalf("DeleteManagedPath: %v", err)
		}

		replacement := importFromOutside(t, st, root, filter, "reused.mkv", "replacement-bytes")
		if replacement.Path != original.Path {
			t.Fatalf("replacement path = %q, want reused %q", replacement.Path, original.Path)
		}
		replacementFile := liveFileAt(t, st, replacement.Path)
		if want := contentFingerprint(t, replacement.Path); replacementFile.ContentFingerprint != want {
			t.Fatalf("replacement fingerprint = %q, want content hash %q", replacementFile.ContentFingerprint, want)
		}
		if replacementFile.ContentFingerprint == originalFile.ContentFingerprint {
			t.Fatalf("replacement kept the original fingerprint %q", originalFile.ContentFingerprint)
		}
		if replacementFile.MediaID == originalFile.MediaID {
			t.Fatalf("replacement kept the original media %q", originalFile.MediaID)
		}
	})
}

// TestManagedConvertIdentityFollowsContent proves the converted artifact is
// cataloged under the digest of its own bytes.
func TestManagedConvertIdentityFollowsContent(t *testing.T) {
	st, root, filter := importStageHarness(t)
	imported := importFromOutside(t, st, root, filter, "clip.mkv", "convert-source")
	result, err := st.ConvertManagedFile(context.Background(), "library-local", imported.Path, "h264-mp4",
		&fakeConverter{container: "mp4"}, filter, claimConvertLease(t, st), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("ConvertManagedFile: %v", err)
	}
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatalf("MediaFilesForLibrary: %v", err)
	}
	var converted *store.MediaFile
	for i := range files {
		if files[i].MediaID == result.Converted.ID {
			converted = &files[i]
		}
	}
	if converted == nil {
		t.Fatalf("no live file for converted media %q", result.Converted.ID)
	}
	if want := contentFingerprint(t, converted.Path); converted.ContentFingerprint != want {
		t.Fatalf("converted fingerprint = %q, want content hash %q", converted.ContentFingerprint, want)
	}
}
