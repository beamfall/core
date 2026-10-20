package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWriteManagedTrashManifestIsAtomic proves a trash manifest is never observed
// partially written: the record is staged in a temp and published by rename, so a
// write that fails cannot truncate the record already on disk and leaves no temp
// residue behind. A torn manifest would strand its entry as neither restorable
// (RestoreManagedPath cannot read it) nor purgeable (the sweep refuses to destroy
// what it cannot prove expired) — hard invariant 6.
func TestWriteManagedTrashManifestIsAtomic(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not deny writes")
	}
	dir := t.TempDir()
	first := managedTrashManifest{
		ID:           "1111",
		OriginalPath: filepath.Join(dir, "original.txt"),
		TrashPath:    filepath.Join(dir, "entry"),
		Kind:         "file",
		DeletedAt:    time.Unix(1700000000, 0).UTC(),
	}
	if err := writeManagedTrashManifest(dir, first); err != nil {
		t.Fatalf("writeManagedTrashManifest: %v", err)
	}
	assertOnlyManifest(t, dir)

	// A failed rewrite must leave the durable record intact, not a partial file.
	//nolint:gosec // G302 does not distinguish directories: a dir needs its
	// execute bit, and 0500 is exactly the read-only-dir state under test.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	//nolint:gosec // G302: restoring owner rwx on a directory, not a file mode.
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	second := first
	second.ID = "2222"
	if err := writeManagedTrashManifest(dir, second); err == nil {
		t.Fatal("writeManagedTrashManifest into a read-only dir: want error")
	}
	//nolint:gosec // G302: restoring owner rwx on a directory, not a file mode.
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := readManagedTrashManifest(dir)
	if err != nil {
		t.Fatalf("readManagedTrashManifest after a failed rewrite: %v", err)
	}
	if got.ID != first.ID || got.OriginalPath != first.OriginalPath || got.Kind != first.Kind {
		t.Fatalf("manifest = %#v, want the intact first record", got)
	}
	assertOnlyManifest(t, dir)
}

func assertOnlyManifest(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "manifest.json" {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("trash dir = %v, want only manifest.json (no temp residue)", names)
	}
}
