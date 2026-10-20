package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAdmitStagedFileNoReplaceRejectsCreateAfterCheck proves the no-replace,
// no-partial admission: when the destination is created AFTER ImportManagedFile's
// preflight Lstat but BEFORE admission, the os.Link finalize fails (EEXIST), the
// pre-existing target's contents are NOT clobbered, and no orphan temp partial is
// left beside it. This closes the create-after-check race the staging design
// guards against.
func TestAdmitStagedFileNoReplaceRejectsCreateAfterCheck(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging.tmp")
	if err := os.WriteFile(staging, []byte("NEW"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "target.mkv")
	// The dst is created between preflight and admit (simulated by it existing now).
	if err := os.WriteFile(dst, []byte("ORIGINAL"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := admitStagedFileNoReplace(staging, dst); err == nil {
		t.Fatal("admitStagedFileNoReplace clobbered an existing target")
	}

	if data, err := os.ReadFile(dst); err != nil || string(data) != "ORIGINAL" {
		t.Fatalf("existing target clobbered: %q (err %v)", data, err)
	}
	// No orphan sibling temp left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name != "staging.tmp" && name != "target.mkv" {
			t.Fatalf("orphan temp left after failed admit: %q", name)
		}
	}
}

// TestAdmitStagedFileNoReplaceSucceedsNoOrphan proves the happy path leaves the
// dst with the staged bytes and removes both the staging source and the sibling
// temp (no orphan partial).
func TestAdmitStagedFileNoReplaceSucceedsNoOrphan(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging.tmp")
	if err := os.WriteFile(staging, []byte("BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "target.mkv")

	if err := admitStagedFileNoReplace(staging, dst); err != nil {
		t.Fatalf("admitStagedFileNoReplace: %v", err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "BYTES" {
		t.Fatalf("dst contents = %q (err %v), want BYTES", data, err)
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Fatalf("staging source not removed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "target.mkv" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dir = %v, want only target.mkv", names)
	}
}
