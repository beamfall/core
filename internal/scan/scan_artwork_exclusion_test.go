package scan

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// writeArtworkVocabularyFixture lays out one video folder, one audio folder
// and one control photo, plus an image for every local artwork vocabulary
// name. It returns the paths that must become catalogue items.
func writeArtworkVocabularyFixture(t *testing.T, root string) []string {
	t.Helper()
	want := []string{
		writeScanFile(t, root, "Clip/Clip.mkv", []byte("video bytes")),
		writeScanFile(t, root, "Record/01 Part.mp3", []byte("audio bytes")),
		// A "-landscape" name with no media file of that stem is a photo,
		// not a sidecar.
		writeScanFile(t, root, "Photos/hills-landscape.jpg", jpegBytes("photo")),
	}
	exts := []string{"jpg", "jpeg", "png", "webp"}
	for i, row := range localArtworkVocabulary {
		ext := exts[i%len(exts)]
		switch row.Scope {
		case localArtworkFile:
			writeScanFile(t, root, "Clip/Clip-"+row.Name+"."+ext, jpegBytes(row.Name))
		case localArtworkFolder:
			writeScanFile(t, root, "Clip/"+row.Name+"."+ext, jpegBytes(row.Name))
			writeScanFile(t, root, "Record/"+strings.ToUpper(row.Name)+"."+ext, jpegBytes(row.Name))
		case localArtworkFolderDir:
			writeScanFile(t, root, "Clip/"+row.Name+"/first."+ext, jpegBytes(row.Name))
		case localArtworkContainer:
			for _, digits := range []string{"01", "123"} {
				writeScanFile(t, root, strings.Replace(row.Name, "00", digits, 1)+"."+ext, jpegBytes(row.Name))
			}
		default:
			t.Fatalf("vocabulary row %+v has an unhandled scope", row)
		}
	}
	// The names the ticket calls out must be in the vocabulary itself.
	for _, name := range []string{"logo", "clearlogo", "banner", "artist", "fanart", "folder", "season00-poster"} {
		if !slices.ContainsFunc(localArtworkVocabulary, func(row localArtworkName) bool { return row.Name == name }) {
			t.Fatalf("vocabulary lacks %q", name)
		}
	}
	return want
}

func liveLibraryPaths(t *testing.T, st *store.Store) []string {
	t.Helper()
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
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

func TestScanNeverCataloguesLocalArtworkSidecars(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		want := writeArtworkVocabularyFixture(t, root)
		slices.Sort(want)
		st := storetest.Open(t)
		if _, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: "library-local", Root: root}, nil); err != nil {
			t.Fatalf("workers=%d scan: %v", workers, err)
		}
		if got := liveLibraryPaths(t, st); !slices.Equal(got, want) {
			t.Fatalf("workers=%d catalogued %q, want only %q", workers, got, want)
		}
	}
}

func TestRescanRemovesPreviouslyCataloguedArtworkSidecars(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	want := writeArtworkVocabularyFixture(t, root)
	slices.Sort(want)
	st := storetest.Open(t)
	// Seed artwork rows the way an older scan catalogued them.
	stale := map[store.MediaID]string{}
	for _, name := range []string{"Clip/clearlogo", "Clip/Clip-poster", "season01-poster", "Record/ARTIST"} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(name)) + ".*")
		if err != nil || len(matches) != 1 {
			t.Fatalf("fixture %s = %q, %v", name, matches, err)
		}
		path := matches[0]
		fingerprint, err := store.HashMediaFileContent(path)
		if err != nil {
			t.Fatalf("hashing %s: %v", name, err)
		}
		media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
			LibraryID:          "library-local",
			Path:               path,
			Title:              titleFromPath(path),
			Container:          containerFromPath(path),
			Class:              store.MediaClassImage,
			ContentFingerprint: fingerprint,
		})
		if err != nil {
			t.Fatalf("seeding %s: %v", name, err)
		}
		stale[media.ID] = path
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if got := liveLibraryPaths(t, st); !slices.Equal(got, want) {
		t.Fatalf("live files after rescan = %q, want only %q", got, want)
	}
	// The rows go through the missing path, not a hard delete.
	for id, path := range stale {
		if _, err := st.Media(context.Background(), id, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}); err != nil {
			t.Fatalf("media for %s was hard-deleted: %v", path, err)
		}
	}
}

func TestIsLocalArtworkSidecar(t *testing.T) {
	dir := t.TempDir()
	writeScanFile(t, dir, "Clip.mkv", []byte("video bytes"))
	writeScanFile(t, dir, "Still.jpg", jpegBytes("photo"))
	cases := map[string]bool{
		"poster.jpg":             true,
		"Folder.JPEG":            true,
		"artist.png":             true,
		"clearlogo.webp":         true,
		"season07-poster.jpg":    true,
		"season2024-poster.jpg":  true,
		"season7-poster.jpg":     false,
		"seasonxx-poster.jpg":    false,
		"Clip-fanart.jpg":        true,
		"clip-LOGO.png":          true,
		"Still-thumb.jpg":        false, // the stem belongs to an image, not playable media
		"other-poster.jpg":       false,
		"poster.gif":             false,
		"poster.mkv":             false,
		"extrafanart/any.jpg":    true,
		"posters/holiday.jpg":    false,
		"season-specials-poster": false,
	}
	for name, want := range cases {
		if got := isLocalArtworkSidecar(filepath.Join(dir, filepath.FromSlash(name))); got != want {
			t.Errorf("isLocalArtworkSidecar(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestRescanDropsArtworkSidecarsThatOutnumberMedia keeps the CORE-0737
// missing-ratio guard from pinning artwork an older scan catalogued: with
// twice as many sidecar rows as media files, the rescan still marks every
// sidecar missing, keeps every clip and logs no guard warning.
func TestRescanDropsArtworkSidecarsThatOutnumberMedia(t *testing.T) {
	// feature:scan-pipeline
	root, st, clips := seedScannedLibrary(t, 10)
	var sidecars []string
	for _, clip := range clips {
		stem := strings.TrimSuffix(clip, filepath.Ext(clip))
		for _, name := range []string{"poster", "fanart"} {
			path := stem + "-" + name + ".jpg"
			writeScanFile(t, root, filepath.Base(path), jpegBytes(filepath.Base(path)))
			fingerprint, err := store.HashMediaFileContent(path)
			if err != nil {
				t.Fatalf("hashing %s: %v", path, err)
			}
			if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
				LibraryID:          "library-local",
				Path:               path,
				Title:              titleFromPath(path),
				Container:          containerFromPath(path),
				Class:              store.MediaClassImage,
				ContentFingerprint: fingerprint,
			}); err != nil {
				t.Fatalf("seeding %s: %v", path, err)
			}
			sidecars = append(sidecars, path)
		}
	}
	if got := len(liveScanPaths(t, st)); got != len(clips)+len(sidecars) {
		t.Fatalf("seeded %d live rows, want %d", got, len(clips)+len(sidecars))
	}
	time.Sleep(2 * time.Millisecond)
	logs := captureScanWarnings(t)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if strings.Contains(logs.String(), "not marking files missing") {
		t.Fatalf("rescan skipped the missing pass: %q", logs.String())
	}
	if got := liveScanPaths(t, st); !slices.Equal(got, clips) {
		t.Fatalf("live files after rescan = %q, want only the clips %q", got, clips)
	}
}
