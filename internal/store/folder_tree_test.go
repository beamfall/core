package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestFolderTreeListsConfiguredRootsAndChildren(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "zeta.txt"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "alpha", "item.mp4"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	tree, err := st.FolderTree(ctx, "library-local", "", 2, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if tree.Name != "Library roots" || len(tree.Children) != 1 {
		t.Fatalf("tree = %#v, want synthetic root with one configured root", tree)
	}
	rootEntry := tree.Children[0]
	if rootEntry.Path != root || rootEntry.Kind != "folder" {
		t.Fatalf("root entry = %#v, want configured folder root", rootEntry)
	}
	if len(rootEntry.Children) != 2 {
		t.Fatalf("root children = %#v, want folder and file", rootEntry.Children)
	}
	if rootEntry.Children[0].Name != "alpha" || rootEntry.Children[0].Kind != "folder" {
		t.Fatalf("first child = %#v, want folders sorted before files", rootEntry.Children[0])
	}
	if rootEntry.Children[1].Name != "zeta.txt" || rootEntry.Children[1].Kind != "file" {
		t.Fatalf("second child = %#v, want file after folders", rootEntry.Children[1])
	}
}

func TestFolderTreeRejectsRootOutsideLibraryRoots(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.FolderTree(ctx, "library-local", outside, 1, store.LibraryFilter{AllLibraries: true}); err == nil {
		t.Fatal("FolderTree outside root succeeded, want containment error")
	}
}

func TestFolderTreeDoesNotFollowChildSymlinks(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := st.SetLibraryRoots(ctx, "library-local", []string{root}); err != nil {
		t.Fatal(err)
	}

	tree, err := st.FolderTree(ctx, "library-local", root, 2, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("children = %#v, want one symlink entry", tree.Children)
	}
	if tree.Children[0].Kind != "file" || len(tree.Children[0].Children) != 0 {
		t.Fatalf("symlink child = %#v, want non-descended file-shaped entry", tree.Children[0])
	}
}
