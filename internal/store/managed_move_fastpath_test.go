package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-1838: every managed relocation clears the stored mtime of the rows it
// repoints, so the next scan's fast-path lookup misses and the file is
// re-derived from its new path instead of skipped as unchanged.
func TestManagedMovesInvalidateScanFastPath(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllLibraries: true}
	importWithModTime := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
			LibraryID: "library-local", Path: path, Title: filepath.Base(path), Container: "mp4", Codec: "h264",
			Class: store.MediaClassVideo, ContentFingerprint: store.HashMediaFilePath(path),
			SizeBytes: int64(len(path)), ModTime: time.Unix(1700000000, 0).UTC(),
		}); err != nil {
			t.Fatalf("ImportMediaFile(%s): %v", path, err)
		}
		if got := fastPathModTime(t, st, path); got.IsZero() {
			t.Fatalf("imported %s has no stored mtime", path)
		}
	}
	assertInvalidated := func(op, path string) {
		t.Helper()
		if got := fastPathModTime(t, st, path); !got.IsZero() {
			t.Fatalf("%s left stored mtime %v at %s, want it cleared", op, got, path)
		}
	}

	folderItem := filepath.Join(root, "Set", "Part1", "item.mp4")
	importWithModTime(folderItem)
	destination := filepath.Join(root, "Set", "Part2")
	if err := os.MkdirAll(destination, 0o750); err != nil {
		t.Fatal(err)
	}
	moved, err := st.MoveManagedPath(ctx, "library-local", filepath.Dir(folderItem), destination, filter)
	if err != nil {
		t.Fatalf("MoveManagedPath: %v", err)
	}
	assertInvalidated("MoveManagedPath", filepath.Join(moved.Path, "item.mp4"))

	single := filepath.Join(root, "single.mp4")
	importWithModTime(single)
	renamed, err := st.RenameManagedPath(ctx, "library-local", single, "single-renamed.mp4", filter)
	if err != nil {
		t.Fatalf("RenameManagedPath: %v", err)
	}
	assertInvalidated("RenameManagedPath", renamed.Path)

	batchOld := filepath.Join(root, "batch.mp4")
	importWithModTime(batchOld)
	batch, err := st.BatchRenameManagedPaths(ctx, "library-local", []store.ManagedPathRename{{Path: batchOld, Name: "batch-renamed.mp4"}}, filter, time.Unix(1700000100, 0))
	if err != nil {
		t.Fatalf("BatchRenameManagedPaths: %v", err)
	}
	assertInvalidated("BatchRenameManagedPaths", filepath.Join(root, "batch-renamed.mp4"))

	// Restore a stored mtime at the renamed path so the undo is what clears it.
	if err := os.WriteFile(filepath.Join(root, "batch-renamed.mp4"), []byte(batchOld), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: filepath.Join(root, "batch-renamed.mp4"), Title: "batch.mp4", Container: "mp4", Codec: "h264",
		Class: store.MediaClassVideo, ContentFingerprint: store.HashMediaFilePath(batchOld),
		SizeBytes: int64(len(batchOld)), ModTime: time.Unix(1700000000, 0).UTC(),
	}); err != nil {
		t.Fatalf("re-import renamed file: %v", err)
	}
	if got := fastPathModTime(t, st, filepath.Join(root, "batch-renamed.mp4")); got.IsZero() {
		t.Fatal("re-import did not restore the stored mtime")
	}
	if _, err := st.UndoBatch(ctx, batch.ID, filter, time.Unix(1700000101, 0)); err != nil {
		t.Fatalf("UndoBatch: %v", err)
	}
	assertInvalidated("UndoBatch", batchOld)
}

func fastPathModTime(t *testing.T, st *store.Store, path string) time.Time {
	t.Helper()
	stored, err := st.MediaFileFingerprintByPath(context.Background(), "library-local", path)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.OK {
		t.Fatalf("no available media_file row at %s", path)
	}
	return stored.ModTime
}
