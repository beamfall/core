package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServerFolderRootsIncludesDarwinVolumes(t *testing.T) {
	oldGOOS, oldVolumesRoot := serverFolderGOOS, serverFolderVolumesRoot
	t.Cleanup(func() {
		serverFolderGOOS = oldGOOS
		serverFolderVolumesRoot = oldVolumesRoot
	})

	volumes := t.TempDir()
	serverFolderGOOS = "darwin"
	serverFolderVolumesRoot = volumes
	if err := os.Mkdir(filepath.Join(volumes, "Media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(volumes, "not-a-volume"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tree, err := serverFolderRoots(1)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, child := range tree.Children {
		paths[child.Path] = true
	}
	if !paths[string(filepath.Separator)] || !paths[filepath.Join(volumes, "Media")] || paths[filepath.Join(volumes, "not-a-volume")] {
		t.Fatalf("roots = %#v, want / and Darwin volume dirs only", tree.Children)
	}
}
