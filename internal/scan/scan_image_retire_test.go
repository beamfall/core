package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

const imageRuleFixturePlugin = "image-rule-fixture-plugin"

// imageRuleLibrary creates a Library bound to the fixture Plugin, or to no
// Plugin when classes is nil, and the Identifier that reports its classes.
func imageRuleLibrary(t *testing.T, st *store.Store, classes []store.MediaClass) (store.LibraryID, *classDeclaringIdentifier) {
	t.Helper()
	pluginID := ""
	if classes != nil {
		pluginID = imageRuleFixturePlugin
	}
	library, err := st.CreateLibrary(context.Background(), "Fixture", false, pluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	return library.ID, &classDeclaringIdentifier{classes: map[string][]store.MediaClass{imageRuleFixturePlugin: classes}}
}

func scanImageRuleLibrary(t *testing.T, st *store.Store, identifier *classDeclaringIdentifier, libraryID store.LibraryID, root string, workers int) {
	t.Helper()
	if _, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, identifier, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root}, nil); err != nil {
		t.Fatalf("workers=%d scan: %v", workers, err)
	}
}

func libraryFilePaths(t *testing.T, st *store.Store, libraryID store.LibraryID) []string {
	t.Helper()
	files, err := st.MediaFilesForLibrary(context.Background(), libraryID)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	slices.Sort(paths)
	return paths
}

// seedImageRow catalogues path as image-class Media the way a scan before
// CORE-2361 did.
func seedImageRow(t *testing.T, st *store.Store, libraryID store.LibraryID, path string) store.MediaID {
	t.Helper()
	fingerprint, err := store.HashMediaFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          libraryID,
		Path:               path,
		Title:              titleFromPath(path),
		Container:          containerFromPath(path),
		Class:              store.MediaClassImage,
		ContentFingerprint: fingerprint,
	})
	if err != nil {
		t.Fatalf("seeding %s: %v", path, err)
	}
	return media.ID
}

// TestScanSkipsStrayImageInVideoLibrary is the CORE-2361 walk acceptance: in a
// Library whose Plugin declares video and not image, neither a recognised
// sidecar nor an unrecognised image becomes an item. A Plugin declaring image,
// and a Library with no Plugin, keep the unrecognised image as an item.
func TestScanSkipsStrayImageInVideoLibrary(t *testing.T) {
	// feature:scan-pipeline
	for _, tc := range []struct {
		name    string
		classes []store.MediaClass
		images  []string
	}{
		{"video plugin", []store.MediaClass{store.MediaClassVideo}, nil},
		{"image plugin", []store.MediaClass{store.MediaClassImage}, []string{"Show/Season 1/season01-banner.jpg", "Show/unknown.jpg"}},
		{"no plugin", nil, []string{"Show/unknown.jpg"}},
	} {
		for _, workers := range []int{1, 4} {
			root := t.TempDir()
			want := []string{writeScanFile(t, root, "Show/Season 1/Show.S01E03.mkv", []byte("episode bytes"))}
			writeScanFile(t, root, "Show/Season 1/season01-banner.jpg", jpegBytes("banner"))
			writeScanFile(t, root, "Show/unknown.jpg", jpegBytes("stray"))
			for _, name := range tc.images {
				want = append(want, filepath.Join(root, filepath.FromSlash(name)))
			}
			slices.Sort(want)
			st := storetest.Open(t)
			libraryID, identifier := imageRuleLibrary(t, st, tc.classes)
			scanImageRuleLibrary(t, st, identifier, libraryID, root, workers)
			if got := libraryFilePaths(t, st, libraryID); !slices.Equal(got, want) {
				t.Fatalf("%s workers=%d catalogued %q, want %q", tc.name, workers, got, want)
			}
		}
	}
}

// CORE-2364: dropping a declared class is not permission to retire its Media.
func TestRescanPreservesPreviouslyCataloguedUndeclaredClasses(t *testing.T) {
	for _, class := range []store.MediaClass{store.MediaClassImage, store.MediaClassAudio} {
		for _, workers := range []int{1, 4} {
			ctx := context.Background()
			root := t.TempDir()
			st := storetest.Open(t)
			libraryID, identifier := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassVideo, class})
			path := writeScanFile(t, root, "existing.jpg", jpegBytes("existing"))
			media, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{LibraryID: libraryID, Path: path, Title: "Existing", Container: "jpg", Codec: "fixture", Class: class, ContentFingerprint: "existing"})
			if err != nil {
				t.Fatal(err)
			}
			// Make the missing pass eligible even though this file is skipped.
			for _, name := range []string{"one.mp4", "two.mp4", "three.mp4"} {
				writeScanFile(t, root, name, []byte(name))
			}
			identifier.classes[imageRuleFixturePlugin] = []store.MediaClass{store.MediaClassVideo}
			time.Sleep(2 * time.Millisecond)
			for range 2 {
				scanImageRuleLibrary(t, st, identifier, libraryID, root, workers)
			}
			files, err := st.MediaFilesForMedia(ctx, media.ID, store.LibraryFilter{AllLibraries: true})
			if err != nil || len(files) != 1 || !files[0].MissingAt.IsZero() {
				t.Fatalf("class %s was retired: %+v, %v", class, files, err)
			}
			trashed, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllLibraries: true})
			if err != nil || len(trashed) != 0 {
				t.Fatalf("undeclared Media trashed: %+v, %v", trashed, err)
			}
		}
	}
}

// TestRescanRetiresNothingWhenTheWalkIsEmpty pins the empty-root guard: a
// root that walks no media files (an unmounted share, say) says nothing about
// which files exist, so a video Library's image rows stay out of the trash
// and the missing pass keeps every row present.
func TestRescanRetiresNothingWhenTheWalkIsEmpty(t *testing.T) {
	// feature:scan-pipeline
	ctx := context.Background()
	root := t.TempDir()
	episode := writeScanFile(t, root, "Show.S01E03.mkv", []byte("episode bytes"))
	stray := writeScanFile(t, root, "unknown.jpg", jpegBytes("stray"))
	st := storetest.Open(t)
	libraryID, identifier := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassVideo})
	scanImageRuleLibrary(t, st, identifier, libraryID, root, 1)
	seedImageRow(t, st, libraryID, stray)
	for _, path := range []string{episode, stray} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2 * time.Millisecond)

	scanImageRuleLibrary(t, st, identifier, libraryID, root, 1)

	trashed, err := st.TrashedMediaList(ctx, store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 0 {
		t.Fatalf("an empty walk retired %v", trashed)
	}
	if got, want := libraryFilePaths(t, st, libraryID), []string{episode, stray}; !slices.Equal(got, want) {
		t.Fatalf("live files after an empty walk = %q, want %q kept present", got, want)
	}
}

func TestImageRuleFor(t *testing.T) {
	for _, tc := range []struct {
		classes []store.MediaClass
		want    ImageRule
	}{
		{nil, ImagesBesideMedia},
		{[]store.MediaClass{store.MediaClassImage}, ImagesAsContent},
		{[]store.MediaClass{store.MediaClassVideo, store.MediaClassImage}, ImagesAsContent},
		{[]store.MediaClass{store.MediaClassVideo}, ImagesAsArtwork},
		{[]store.MediaClass{store.MediaClassAudio}, ImagesAsArtwork},
	} {
		if got := ImageRuleFor(tc.classes); got != tc.want {
			t.Errorf("ImageRuleFor(%v) = %v, want %v", tc.classes, got, tc.want)
		}
	}
}
