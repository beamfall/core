package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestBrowseServerFoldersListsDirectoriesOnly(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "music"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	// EvalSymlinks canonicalizes the path (on macOS /var → /private/var), so
	// compare against the resolved form rather than the raw temp path.
	wantPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := st.BrowseServerFolders(ctx, root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Path != wantPath || tree.Kind != "folder" {
		t.Fatalf("tree = %#v, want folder rooted at %s", tree, wantPath)
	}
	if len(tree.Children) != 2 {
		t.Fatalf("children = %#v, want only the two directories (no file)", tree.Children)
	}
	for _, c := range tree.Children {
		if c.Kind != "folder" {
			t.Fatalf("child %#v is not a folder — files must be excluded", c)
		}
	}
	if tree.Children[0].Name != "movies" || tree.Children[1].Name != "music" {
		t.Fatalf("children = %#v, want case-insensitive name order movies,music", tree.Children)
	}
}

func TestBrowseServerFoldersEmptyPathListsFilesystemRoot(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)

	tree, err := st.BrowseServerFolders(ctx, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Path != string(filepath.Separator) {
		t.Fatalf("empty path tree.Path = %q, want filesystem root", tree.Path)
	}
	if len(tree.Children) == 0 {
		t.Fatal("filesystem root listed no directories")
	}
	if runtime.GOOS == "linux" {
		for _, c := range tree.Children {
			switch c.Path {
			case "/proc", "/sys", "/dev", "/run", "/snap":
				t.Fatalf("pseudo-filesystem %s must be skipped at the root", c.Path)
			}
		}
	}
}

func TestBrowseServerFoldersFollowsSymlinkedDir(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	realTarget := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realTarget, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(realTarget, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	tree, err := st.BrowseServerFolders(ctx, parent, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 || tree.Children[0].Name != "linked" || tree.Children[0].Kind != "folder" {
		t.Fatalf("children = %#v, want the symlinked dir surfaced as a folder", tree.Children)
	}
}

func TestBrowseServerFoldersRejectsMissingPath(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := st.BrowseServerFolders(ctx, missing, 1)
	var verr store.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want ValidationError for a missing path", err)
	}
}

func TestBrowseServerFoldersRejectsFilePath(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := st.BrowseServerFolders(ctx, file, 1)
	var verr store.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want ValidationError when the path is a file", err)
	}
}
