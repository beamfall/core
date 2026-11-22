package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	audiobook "github.com/beamfall/plugin-audiobook"
)

// scanDiscBook scans root as an audiobook library with the real plugin parser
// and returns the Media holding the files under book, failing unless they all
// share one Media, in that Media's part order.
func scanDiscBook(t *testing.T, root string, workers int, want []string) {
	t.Helper()
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}}
	parsers := map[string]FilenameParser{audiobook.PluginID: audiobook.New()}
	for pass := 1; pass <= 2; pass++ {
		if _, err := ScanWithWorkerGovernor(context.Background(), st, taggedPartsProber{}, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root}); err != nil {
			t.Fatalf("workers=%d pass=%d scan: %v", workers, pass, err)
		}
		files, err := st.MediaFilesForLibrary(context.Background(), library.ID)
		if err != nil {
			t.Fatal(err)
		}
		byPath := map[string]store.MediaID{}
		for _, file := range files {
			byPath[file.Path] = file.MediaID
		}
		mediaID := byPath[want[0]]
		for _, path := range want {
			if byPath[path] != mediaID || mediaID == "" {
				t.Fatalf("workers=%d pass=%d %q in Media %q, want %q with %q; files %#v", workers, pass, path, byPath[path], mediaID, want[0], files)
			}
		}
		grouped, err := st.MediaFilesForMedia(context.Background(), mediaID, filter)
		if err != nil || len(grouped) != len(want) {
			t.Fatalf("workers=%d pass=%d grouped = %#v, %v; want %d parts", workers, pass, grouped, err, len(want))
		}
		for i, file := range grouped {
			if file.Path != want[i] || file.PartOrdinal != i {
				t.Fatalf("workers=%d pass=%d part %d = {path:%q ordinal:%d}, want {path:%q ordinal:%d}", workers, pass, i, file.Path, file.PartOrdinal, want[i], i)
			}
		}
	}
}

// TestScanKeepsDiscBookAcrossInterleavedSiblingFile (CORE-3017): a loose file
// in the book folder that sorts between two disc folders does not split the
// disc folders into two Media.
func TestScanKeepsDiscBookAcrossInterleavedSiblingFile(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		book := filepath.Join(root, "Writer", "Novel")
		want := []string{
			writeScanFile(t, book, "CD1/01.mp3", []byte("d1t1")),
			writeScanFile(t, book, "CD1/02.mp3", []byte("d1t2")),
			writeScanFile(t, book, "Disc 2/01.mp3", []byte("d2t1")),
			writeScanFile(t, book, "Disc 2/02.mp3", []byte("d2t2")),
		}
		writeScanFile(t, book, "Credits.mp3", []byte("loose"))
		scanDiscBook(t, root, workers, want)
	}
}

// TestScanOrdersDiscFoldersNamingTheSameDisc (CORE-3017): "CD1" and "Disc 01"
// both name disc 1; their tracks still join one Media without colliding.
func TestScanOrdersDiscFoldersNamingTheSameDisc(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		book := filepath.Join(root, "Writer", "Novel")
		want := []string{
			writeScanFile(t, book, "CD1/01.mp3", []byte("a1")),
			writeScanFile(t, book, "CD1/02.mp3", []byte("a2")),
			writeScanFile(t, book, "Disc 01/01.mp3", []byte("b1")),
			writeScanFile(t, book, "Disc 01/02.mp3", []byte("b2")),
		}
		scanDiscBook(t, root, workers, want)
	}
}
