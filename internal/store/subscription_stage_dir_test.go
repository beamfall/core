package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSameDirectoryComparesIdentityNotSpelling(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "shows")
	if err := os.Mkdir(real, 0o750); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	if !sameDirectory(real, alias) || !sameDirectory(real+"/", real) {
		t.Fatal("one directory under two spellings must compare equal")
	}
	other := filepath.Join(base, "other")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	if sameDirectory(real, other) || sameDirectory(real, filepath.Join(base, "missing")) {
		t.Fatal("distinct or missing directories must not compare equal")
	}
}
