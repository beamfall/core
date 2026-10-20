package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPathPickerAllowedRootsRejectBlocklistOnOpen(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir(), Options{AllowedRoots: []string{"/etc"}})
	if !errors.Is(err, ValidationError("path is blocklisted")) {
		t.Fatalf("Open err = %v, want blocklisted validation", err)
	}
}

func TestPathPickerAddRootPersistsCanonicalRoot(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "media-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	canonicalRoot, err := canonicalExistingPath(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.AddPathPickerRoot(ctx, link)
	if err != nil {
		t.Fatalf("AddPathPickerRoot: %v", err)
	}
	if added.Path != canonicalRoot {
		t.Fatalf("added.Path = %q, want canonical %q", added.Path, canonicalRoot)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	picker, err := reopened.PathPicker(ctx, PathPickerRequest{Role: PathRoleLibraryRoot})
	if err != nil {
		t.Fatalf("PathPicker: %v", err)
	}
	if !pathPickerRootContains(picker.Roots, canonicalRoot) {
		t.Fatalf("roots = %#v, want persisted %q", picker.Roots, canonicalRoot)
	}
}

func TestPathPickerAddRootRejectsSwapBeforeOpenRoot(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	root := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := canonicalExistingPath(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	oldOpenRoot := pathPickerOpenRoot
	pathPickerOpenRoot = func(name string) (*os.Root, error) {
		if name == canonicalRoot {
			if err := os.RemoveAll(canonicalRoot); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(canonicalRoot, []byte("not a directory"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return oldOpenRoot(name)
	}
	t.Cleanup(func() { pathPickerOpenRoot = oldOpenRoot })

	if _, err := st.AddPathPickerRoot(ctx, root); err == nil {
		t.Fatal("AddPathPickerRoot succeeded after root was swapped before open")
	}
	picker, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleLibraryRoot})
	if err != nil {
		t.Fatalf("PathPicker: %v", err)
	}
	if pathPickerRootContains(picker.Roots, canonicalRoot) {
		t.Fatalf("roots = %#v, swapped root was persisted", picker.Roots)
	}
}

func TestPathPickerAddRootRejectsDirectorySwapBeforeOpenRoot(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	parent := t.TempDir()
	root := filepath.Join(parent, "media")
	replacement := filepath.Join(parent, "replacement")
	for _, dir := range []string{root, replacement} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	canonicalRoot, err := canonicalExistingPath(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, dataDir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	oldOpenRoot := pathPickerOpenRoot
	pathPickerOpenRoot = func(name string) (*os.Root, error) {
		if name == canonicalRoot {
			if err := os.RemoveAll(canonicalRoot); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, canonicalRoot); err != nil {
				t.Fatal(err)
			}
		}
		return oldOpenRoot(name)
	}
	t.Cleanup(func() { pathPickerOpenRoot = oldOpenRoot })

	if _, err := st.AddPathPickerRoot(ctx, root); !errors.Is(err, ValidationError("folder changed while adding root")) {
		t.Fatalf("AddPathPickerRoot err = %v, want folder changed validation", err)
	}
	picker, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleLibraryRoot})
	if err != nil {
		t.Fatalf("PathPicker: %v", err)
	}
	if pathPickerRootContains(picker.Roots, canonicalRoot) {
		t.Fatalf("roots = %#v, swapped root was persisted", picker.Roots)
	}
}

func pathPickerRootContains(roots []PathPickerRoot, path string) bool {
	for _, root := range roots {
		if root.Path == path {
			return true
		}
	}
	return false
}

func TestPathPickerRoleEligibilityAndPreview(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	libraryRoot := filepath.Join(base, "library")
	sibling := filepath.Join(base, "incoming")
	if err := os.MkdirAll(libraryRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pathPickerMaxPreview+1; i++ {
		if err := os.WriteFile(filepath.Join(libraryRoot, "clip-"+string(rune('a'+i))+".mp4"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(ctx, t.TempDir(), Options{SeedDevelopmentData: true, AllowedRoots: []string{base}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetLibraryRoots(ctx, "library-local", []string{libraryRoot}); err != nil {
		t.Fatalf("SetLibraryRoots: %v", err)
	}
	inside, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleIncomingFolder, LibraryID: "library-local", Path: libraryRoot, Depth: 1})
	if err != nil {
		t.Fatalf("inside PathPicker: %v", err)
	}
	if len(inside.Entries) != 1 || !inside.Entries[0].Selectable || inside.Entries[0].MediaCount != pathPickerMaxPreview+1 || !inside.Entries[0].Truncated {
		t.Fatalf("inside entry = %#v", inside.Entries)
	}
	if got := len(inside.Entries[0].SampleNames); got != pathPickerMaxPreview {
		t.Fatalf("sample count = %d, want %d", got, pathPickerMaxPreview)
	}
	outside, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleIncomingFolder, LibraryID: "library-local", Path: sibling, Depth: 0})
	if err != nil {
		t.Fatalf("outside PathPicker: %v", err)
	}
	if len(outside.Entries) != 1 || outside.Entries[0].Selectable || outside.Entries[0].ReasonCode != "not-inside-library-root" {
		t.Fatalf("outside entry = %#v, want not-inside-library-root", outside.Entries)
	}
	for _, role := range []string{PathRoleLibraryRoot, PathRoleDownloadDestination, PathRoleBackupDestination} {
		picker, err := st.PathPicker(ctx, PathPickerRequest{Role: role, Path: sibling, Depth: 0})
		if err != nil {
			t.Fatalf("%s PathPicker: %v", role, err)
		}
		if len(picker.Entries) != 1 || !picker.Entries[0].Selectable || picker.Entries[0].ReasonCode != "" {
			t.Fatalf("%s entry = %#v, want selectable without role-specific rejection", role, picker.Entries)
		}
	}
}

func TestPathPickerAppStoreSuppressesDownloadNamedRoots(t *testing.T) {
	ctx := context.Background()
	oldGOOS, oldVolumesRoot := pathPickerGOOS, pathPickerVolumesRoot
	t.Cleanup(func() {
		pathPickerGOOS = oldGOOS
		pathPickerVolumesRoot = oldVolumesRoot
	})
	pathPickerGOOS = "darwin"
	pathPickerVolumesRoot = t.TempDir()
	goodRoot := filepath.Join(pathPickerVolumesRoot, "Media Vault")
	downloadRoot := filepath.Join(pathPickerVolumesRoot, "TIDAL-Downloader-NG")
	if err := os.MkdirAll(goodRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(downloadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalGoodRoot, err := canonicalExistingPath(goodRoot)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDownloadRoot, err := canonicalExistingPath(downloadRoot)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(ctx, t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	normal, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleLibraryRoot})
	if err != nil {
		t.Fatalf("normal PathPicker: %v", err)
	}
	if !pathPickerRootContains(normal.Roots, canonicalDownloadRoot) {
		t.Fatalf("normal roots = %#v, want download-named mount visible outside App Store mode", normal.Roots)
	}
	appStore, err := st.PathPicker(ctx, PathPickerRequest{Role: PathRoleLibraryRoot, DistributionClass: "appStore"})
	if err != nil {
		t.Fatalf("appStore PathPicker: %v", err)
	}
	if pathPickerRootContains(appStore.Roots, canonicalDownloadRoot) {
		t.Fatalf("appStore roots = %#v, want download-named mount suppressed", appStore.Roots)
	}
	if !pathPickerRootContains(appStore.Roots, canonicalGoodRoot) {
		t.Fatalf("appStore roots = %#v, want unrelated mount retained", appStore.Roots)
	}
	for _, role := range appStore.Roles {
		if role == PathRoleDownloadDestination {
			t.Fatalf("appStore roles = %#v, want downloadDestination suppressed", appStore.Roles)
		}
	}
}

func TestPathPickerPreviewTimeoutTruncates(t *testing.T) {
	oldReadDir, oldTimeout := pathPickerReadDir, pathPickerPreviewTimeoutDuration
	block := make(chan struct{})
	done := make(chan struct{})
	pathPickerReadDir = func(string) ([]os.DirEntry, error) {
		defer close(done)
		<-block
		return nil, nil
	}
	pathPickerPreviewTimeoutDuration = time.Nanosecond
	t.Cleanup(func() {
		close(block)
		<-done
		pathPickerReadDir = oldReadDir
		pathPickerPreviewTimeoutDuration = oldTimeout
	})

	count, samples, truncated := previewPathPickerFolder(context.Background(), t.TempDir())
	if count != 0 || len(samples) != 0 || !truncated {
		t.Fatalf("preview = count %d samples %#v truncated %v, want timeout truncation", count, samples, truncated)
	}
}

func TestPathPickerRuntimeDetection(t *testing.T) {
	oldDocker, oldPodman, oldCgroup := pathPickerDockerEnv, pathPickerPodmanEnv, pathPickerCgroupPath
	t.Cleanup(func() {
		pathPickerDockerEnv, pathPickerPodmanEnv, pathPickerCgroupPath = oldDocker, oldPodman, oldCgroup
	})
	dir := t.TempDir()
	pathPickerDockerEnv = filepath.Join(dir, "missing-docker")
	pathPickerPodmanEnv = filepath.Join(dir, "missing-podman")
	pathPickerCgroupPath = filepath.Join(dir, "cgroup")
	if err := os.WriteFile(pathPickerCgroupPath, []byte("0::/kubepods.slice"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectPathPickerRuntime(); got != PathPickerRuntimeContainer {
		t.Fatalf("runtime = %q, want container", got)
	}
}
