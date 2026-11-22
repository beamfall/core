package scan

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
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
		// Two untagged loose files make the plugin refuse the book folder's
		// own files; the refusals must not split the book either.
		writeScanFile(t, book, "Cover Notes.mp3", []byte("loose two"))
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

// TestScanRebuildsEmbeddedChaptersWhenDiscsRegroupOnFastPath (CORE-3093):
// two previously separate Media with one embedded chapter each folded into
// one group on an unchanged-file rescan rebuild chapter-import markers across
// all Parts and keep them across a subsequent fast-path rescan without probing.
func TestScanRebuildsEmbeddedChaptersWhenDiscsRegroupOnFastPath(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		book := filepath.Join(root, "Writer", "Novel")
		disc1 := writeScanFile(t, book, "CD1/01.mp3", []byte("disc one audio"))
		disc2 := writeScanFile(t, book, "Disc 2/01.mp3", []byte("disc two audio"))

		st := storetest.Open(t)
		library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}}
		prober := &pathProber{probes: map[string]ffmpeg.Probe{
			disc1: {
				Container:       "mp3",
				AudioCodec:      "mp3",
				DurationSeconds: 60,
				Chapters:        []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 60, Title: "Disc 1 Opening"}},
			},
			disc2: {
				Container:       "mp3",
				AudioCodec:      "mp3",
				DurationSeconds: 90,
				Chapters:        []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 90, Title: "Disc 2 Opening"}},
			},
		}}

		// Initial scan without the grouping parser produces two separate Media,
		// each with its own single embedded chapter marker.
		if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, nil, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root}); err != nil {
			t.Fatalf("workers=%d initial scan: %v", workers, err)
		}
		files, err := st.MediaFilesForLibrary(context.Background(), library.ID)
		if err != nil || len(files) != 2 {
			t.Fatalf("workers=%d initial files = %#v, %v; want 2", workers, files, err)
		}
		byPath := map[string]store.MediaID{}
		for _, file := range files {
			byPath[file.Path] = file.MediaID
		}
		if byPath[disc1] == "" || byPath[disc2] == "" || byPath[disc1] == byPath[disc2] {
			t.Fatalf("workers=%d initial Media IDs = %#v, want two separate Media", workers, byPath)
		}
		for _, path := range []string{disc1, disc2} {
			markers, err := st.MarkersForMedia(context.Background(), byPath[path], filter)
			if err != nil || len(markers) != 1 || markers[0].Method != "chapter-import" {
				t.Fatalf("workers=%d initial markers for %q = %+v, %v; want 1 chapter-import marker", workers, path, markers, err)
			}
		}

		// Rescan 1: unchanged files folded into one group by plugin-audiobook.
		prober.calls = nil
		parsers := map[string]FilenameParser{audiobook.PluginID: audiobook.New()}
		if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root}); err != nil {
			t.Fatalf("workers=%d regroup rescan: %v", workers, err)
		}
		if len(prober.calls) != 2 {
			t.Fatalf("workers=%d regroup probe calls = %v, want 2 parts probed", workers, prober.calls)
		}
		files, err = st.MediaFilesForLibrary(context.Background(), library.ID)
		if err != nil || len(files) != 2 {
			t.Fatalf("workers=%d regroup files = %#v, %v; want 2", workers, files, err)
		}
		mergedID := files[0].MediaID
		if files[1].MediaID != mergedID {
			t.Fatalf("workers=%d regrouped files still split across %q and %q", workers, files[0].MediaID, files[1].MediaID)
		}
		grouped, err := st.MediaFilesForMedia(context.Background(), mergedID, filter)
		if err != nil || len(grouped) != 2 || grouped[0].Path != disc1 || grouped[1].Path != disc2 {
			t.Fatalf("workers=%d grouped parts = %#v, %v; want [%q, %q]", workers, grouped, err, disc1, disc2)
		}

		// Rescan 2: steady-state unchanged fast path keeps both markers with 0 probes.
		noProbe := &countingProber{}
		if _, err := ScanWithWorkerGovernor(context.Background(), st, noProbe, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root}); err != nil {
			t.Fatalf("workers=%d steady rescan: %v", workers, err)
		}
		if noProbe.calls != 0 {
			t.Fatalf("workers=%d steady fast-path probe calls = %d, want 0", workers, noProbe.calls)
		}

		markers, err := st.MarkersForMedia(context.Background(), mergedID, filter)
		if err != nil {
			t.Fatal(err)
		}
		wantTitles := []string{"Disc 1 Opening", "Disc 2 Opening"}
		wantStarts := []float64{0, 60}
		wantEnds := []float64{60, 150}
		if len(markers) != 2 {
			t.Fatalf("workers=%d merged markers = %+v, want 2 chapter-import markers", workers, markers)
		}
		for i, marker := range markers {
			if marker.Method != "chapter-import" || marker.Title != wantTitles[i] || marker.StartSeconds != wantStarts[i] || marker.EndSeconds == nil || *marker.EndSeconds != wantEnds[i] {
				t.Fatalf("workers=%d marker %d = %+v, want chapter-import %q %.0f-%.0f", workers, i, marker, wantTitles[i], wantStarts[i], wantEnds[i])
			}
		}
	}
}
