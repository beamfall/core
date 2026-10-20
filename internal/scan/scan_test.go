package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	audiobook "github.com/beamfall/plugin-audiobook"
	filmtv "github.com/beamfall/plugin-filmtv"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/playback"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/streamtoken"
)

func TestScanImportsFilesByContentFingerprint(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	first := writeScanFile(t, root, "first.mp4", []byte("same content"))
	moved := writeScanFile(t, root, "nested/moved.mp4", []byte("same content"))
	second := writeScanFile(t, root, "second.mp4", []byte("different content"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	if result.FilesSeen != 3 {
		t.Fatalf("FilesSeen = %d, want 3", result.FilesSeen)
	}
	if result.MediaImported != 2 {
		t.Fatalf("MediaImported = %d, want 2", result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "moved") {
		t.Fatalf("sourceItems missing moved import after duplicate path update: %#v", items)
	}
	if !hasSourceItemWithTitle(items, "second") {
		t.Fatalf("sourceItems missing second import: %#v", items)
	}

	media, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               moved,
		Title:              "moved",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: result.Files[duplicatePathIndex(result.Files, moved)].ContentFingerprint,
	})
	if err != nil {
		t.Fatalf("re-import moved file: %v", err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), media.ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.Path != moved {
		t.Fatalf("duplicate path = %q, want %q", file.Path, moved)
	}
	if first == second {
		t.Fatal("test setup created identical paths")
	}
}

func TestScanSkipsDotfilesAndDoesNotAbort(t *testing.T) {
	// feature:scan-pipeline
	// Regression: a real directory carries junk the scan must survive. A
	// leading-dot name (".DS_Store", …) derives an empty title the store
	// rejects, which previously aborted the ENTIRE walk with "media title is
	// required" — so one macOS metadata file left the whole Library empty. The
	// scan must skip such files and still import the real media beside them.
	root := t.TempDir()
	writeScanFile(t, root, ".DS_Store", []byte("macos junk"))
	writeScanFile(t, root, "sub/.DS_Store", []byte("macos junk"))
	writeScanFile(t, root, "movie.mp4", []byte("video bytes"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error (a dotfile must not abort the walk): %v", err)
	}
	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1 (the movie; both .DS_Store skipped)", result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "movie") {
		t.Fatalf("sourceItems missing the movie import: %#v", items)
	}
	if hasSourceItemWithTitle(items, "") {
		t.Fatalf("an empty-title (.DS_Store) import leaked into the Library: %#v", items)
	}
}

func TestScanSurvivesUnreadableFileMidWalk(t *testing.T) {
	// feature:scan-pipeline
	// Regression (tests.md): a single permission-denied file mid-walk must not
	// abort the Library scan. The resilient WalkDir wrapper logs and skips the
	// unreadable file (its open/hash fails with EACCES) so the rest of the tree
	// still imports. Root bypasses file permissions — a mode-000 file would be
	// readable and import — so the permission path can only be exercised
	// unprivileged; skip otherwise.
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode-000 files stay readable, so the permission-denied path is unreachable")
	}
	root := t.TempDir()
	// "denied.mp4" sorts before "readable.mp4", so WalkDir hits the unreadable
	// file first — proving its failure does not stop the subsequent import.
	denied := writeScanFile(t, root, "denied.mp4", []byte("secret video bytes"))
	writeScanFile(t, root, "readable.mp4", []byte("video bytes"))
	// writeScanFile already backdated the mtime past the stability window, so
	// the open/hash step (not the stability gate) is what fails here.
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatal(err)
	}
	// Restore owner read so t.TempDir's cleanup is unhindered on all platforms.
	t.Cleanup(func() { _ = os.Chmod(denied, 0o600) })

	st := storetest.Open(t)
	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error (an unreadable file must not abort the walk): %v", err)
	}
	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1 (the readable movie; denied.mp4 skipped)", result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "readable") {
		t.Fatalf("sourceItems missing the readable import after the unreadable file: %#v", items)
	}
	if hasSourceItemWithTitle(items, "denied") {
		t.Fatalf("the mode-000 file was imported despite being unreadable: %#v", items)
	}
}

func TestScanSkipsNonMediaExtensions(t *testing.T) {
	// feature:scan-pipeline
	// Regression guard for master #43: a folder containing mixed media and
	// non-media files must import only the known media containers; documents,
	// archives, and no-extension files must be silently dropped. Images now
	// import (ADR-0043 image Media) — see cover.jpg below.
	root := t.TempDir()
	writeScanFile(t, root, "movie.mp4", []byte("video bytes"))
	writeScanFile(t, root, "song.flac", []byte("audio bytes"))
	writeScanFile(t, root, "cover.jpg", []byte("image — ADR-0043 image Media, imported"))
	writeScanFile(t, root, "readme.txt", []byte("text"))
	writeScanFile(t, root, "archive.zip", []byte("archive"))
	writeScanFile(t, root, "metadata.nfo", []byte("nfo"))
	writeScanFile(t, root, "noext", []byte("no extension"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.FilesSeen != 3 {
		t.Fatalf("FilesSeen = %d, want 3 (mp4 + flac + jpg)", result.FilesSeen)
	}
	if result.MediaImported != 3 {
		t.Fatalf("MediaImported = %d, want 3", result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"movie", "song", "cover"} {
		if !hasSourceItemWithTitle(items, title) {
			t.Fatalf("sourceItems missing the %q import: %#v", title, items)
		}
	}
	for _, title := range []string{"readme", "archive", "metadata", "noext"} {
		if hasSourceItemWithTitle(items, title) {
			t.Fatalf("non-media file %q was imported into the Library", title)
		}
	}
}

func TestScanAllNonMediaDirectorySucceedsEmpty(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "readme.txt", []byte("text"))
	writeScanFile(t, root, "archive.zip", []byte("archive"))
	writeScanFile(t, root, "metadata.nfo", []byte("nfo"))
	writeScanFile(t, root, "noext", []byte("no extension"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.FilesSeen != 0 || result.MediaImported != 0 || len(result.Files) != 0 {
		t.Fatalf("result = %+v, want empty successful scan", result)
	}
}

type fakeProber struct {
	probe ffmpeg.Probe
	err   error
}

func (f fakeProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	return f.probe, f.err
}

type fakeLocalExtractor struct {
	fields   map[string]any
	entities []LocalEntity
	calls    []string
}

func (f *fakeLocalExtractor) ExtractLocal(_ context.Context, class, _, path string) (LocalMetadata, error) {
	f.calls = append(f.calls, class+"|"+filepath.Base(path))
	return LocalMetadata{
		Fields:             f.fields,
		ImpliedMediaFields: map[string]string{"class": "photo"},
		Entities:           f.entities,
	}, nil
}

func mediaFieldMap(fields []store.MediaField) map[string]store.MediaField {
	out := map[string]store.MediaField{}
	for _, field := range fields {
		out[field.Name] = field
	}
	return out
}

func sourceItemByTitle(items []store.SourceItem, title string) *store.SourceItem {
	for i := range items {
		if items[i].Title == title {
			return &items[i]
		}
	}
	return nil
}

func sourceItemIDs(items []store.SourceItem) []store.MediaID {
	ids := make([]store.MediaID, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func TestScanUsesProbedMetadata(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "clip.mkv", []byte("matroska bytes"))
	st := storetest.Open(t)

	prober := fakeProber{probe: ffmpeg.Probe{Container: "matroska", VideoCodec: "hevc", DurationSeconds: 3600}}
	result, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if media.Container != "matroska" || media.Codec != "hevc" || media.DurationSeconds != 3600 {
		t.Fatalf("media = container %q codec %q duration %d, want matroska/hevc/3600", media.Container, media.Codec, media.DurationSeconds)
	}
}

func TestScanImportsAudiobookNarratorFromProbeTags(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Novel/Novel.mp3", []byte("audiobook bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	prober := fakeProber{probe: ffmpeg.Probe{
		Container:  "mp3",
		AudioCodec: "mp3",
		Tags:       map[string]string{"narrator": "Julia Whelan"},
	}}
	parsers := map[string]FilenameParser{audiobook.PluginID: audiobook.New()}
	result, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	fields, err := st.MediaFields(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got := mediaFieldMap(fields)["narrator"]
	if got.Text != "Julia Whelan" {
		t.Fatalf("narrator field = %+v, want Julia Whelan", got)
	}
}

func TestScanImportsM4BAudiobookChaptersAsMarkers(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Novel/Novel.m4b", []byte("audiobook bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	prober := fakeProber{probe: ffmpeg.Probe{
		Container:  "m4b",
		AudioCodec: "aac",
		Chapters: []ffmpeg.ProbeChapter{
			{StartSeconds: 0, EndSeconds: 64.4, Title: "Opening"},
			{StartSeconds: 64.4, EndSeconds: 128.9, Title: "The Door"},
		},
	}}
	result, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	markers, err := st.MarkersForMedia(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 2 {
		t.Fatalf("len(markers) = %d, want 2: %+v", len(markers), markers)
	}
	for i, marker := range markers {
		if marker.Provenance != store.ProvenanceDetected || marker.Method != "chapter-import" || marker.Confidence != 1 {
			t.Fatalf("marker %d provenance = %+v, want detected chapter-import confidence 1", i, marker)
		}
		if marker.SegmentKind != "" || len(marker.Tags) != 0 {
			t.Fatalf("marker %d shape = kind %q tags %v, want plain titled chapter marker", i, marker.SegmentKind, marker.Tags)
		}
	}
	if markers[0].Title != "Opening" || markers[0].StartSeconds != 0 || *markers[0].EndSeconds != 64.4 {
		t.Fatalf("marker 0 = %+v, want Opening 0-64.4", markers[0])
	}
	if markers[1].Title != "The Door" || markers[1].StartSeconds != 64.4 || *markers[1].EndSeconds != 128.9 {
		t.Fatalf("marker 1 = %+v, want The Door 64.4-128.9", markers[1])
	}
}

func TestScanImportsEmbeddedChaptersForParserGroupedM4B(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Author/Novel/1 - First.m4b", []byte("first grouped audiobook bytes"))
	writeScanFile(t, root, "Author/Novel/2 - Second.m4b", []byte("second grouped audiobook bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	prober := &countingProber{probe: ffmpeg.Probe{
		Container:       "m4b",
		AudioCodec:      "aac",
		DurationSeconds: 120,
		Chapters: []ffmpeg.ProbeChapter{
			{StartSeconds: 0, EndSeconds: 55, Title: "Opening"},
			{StartSeconds: 55, EndSeconds: 120, Title: "The Door"},
		},
	}}
	result, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	markers, err := st.MarkersForMedia(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 4 {
		t.Fatalf("markers = %+v, want two embedded chapters from each Part", markers)
	}
	wantTitles := []string{"Opening", "The Door", "Opening", "The Door"}
	wantStarts := []float64{0, 55, 120, 175}
	wantEnds := []float64{55, 120, 175, 240}
	for i, marker := range markers {
		if marker.Method != "chapter-import" || marker.Title != wantTitles[i] || marker.StartSeconds != wantStarts[i] || marker.EndSeconds == nil || *marker.EndSeconds != wantEnds[i] {
			t.Fatalf("marker %d = %+v, want embedded %q %.0f-%.0f", i, marker, wantTitles[i], wantStarts[i], wantEnds[i])
		}
	}

	noProbe := &countingProber{}
	if _, err := Scan(context.Background(), st, noProbe, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	}); err != nil {
		t.Fatalf("fast-path Scan returned error: %v", err)
	}
	if noProbe.calls != 0 {
		t.Fatalf("fast-path probe calls = %d, want 0 with persisted embedded chapters", noProbe.calls)
	}
}

func TestScanRecomputesGroupedPartMarkersWhenEmbeddedChaptersChange(t *testing.T) {
	tests := []struct {
		name            string
		initialChapters []ffmpeg.ProbeChapter
		changedChapters []ffmpeg.ProbeChapter
		initialMethod   string
		initialCount    int
		wantMethod      string
		wantCount       int
	}{
		{
			name:            "gain embedded chapters",
			changedChapters: []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 60, Title: "New Chapter"}},
			initialMethod:   partBoundaryMarkerMethod,
			initialCount:    2,
			wantMethod:      "chapter-import",
			wantCount:       1,
		},
		{
			name:            "lose embedded chapters",
			initialChapters: []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 60, Title: "Old Chapter"}},
			initialMethod:   "chapter-import",
			initialCount:    1,
			wantMethod:      partBoundaryMarkerMethod,
			wantCount:       2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			firstPath := writeScanFile(t, root, "Author/Novel/1 - First.m4b", []byte("first version"))
			secondPath := writeScanFile(t, root, "Author/Novel/2 - Second.m4b", []byte("unchanged second version"))
			st := storetest.Open(t)
			library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			prober := &pathProber{probes: map[string]ffmpeg.Probe{
				firstPath:  {Container: "m4b", AudioCodec: "aac", DurationSeconds: 60, Chapters: tt.initialChapters},
				secondPath: {Container: "m4b", AudioCodec: "aac", DurationSeconds: 60},
			}}
			parsers := map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}
			result, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
			if err != nil {
				t.Fatalf("initial Scan returned error: %v", err)
			}
			mediaID := result.Files[0].MediaID
			markers, err := st.MarkersForMedia(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if len(markers) != tt.initialCount {
				t.Fatalf("initial markers = %+v, want %d %s markers", markers, tt.initialCount, tt.initialMethod)
			}
			for _, marker := range markers {
				if marker.Method != tt.initialMethod {
					t.Fatalf("initial marker = %+v, want method %q", marker, tt.initialMethod)
				}
			}

			writeScanFile(t, root, "Author/Novel/1 - First.m4b", []byte("changed first version with a different size"))
			prober.probes[firstPath] = ffmpeg.Probe{Container: "m4b", AudioCodec: "aac", DurationSeconds: 60, Chapters: tt.changedChapters}
			prober.calls = nil
			if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root}); err != nil {
				t.Fatalf("transition Scan returned error: %v", err)
			}
			if len(prober.calls) != 2 {
				t.Fatalf("transition probe calls = %v, want changed Part plus fast-path Part", prober.calls)
			}
			markers, err = st.MarkersForMedia(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if len(markers) != tt.wantCount {
				t.Fatalf("transition markers = %+v, want %d %s markers", markers, tt.wantCount, tt.wantMethod)
			}
			for _, marker := range markers {
				if marker.Method != tt.wantMethod {
					t.Fatalf("transition marker = %+v, want method %q with no stale rows", marker, tt.wantMethod)
				}
			}
		})
	}
}

func TestScanDoesNotClobberCuratedAudiobookNarrator(t *testing.T) {
	root := t.TempDir()
	path := writeScanFile(t, root, "Novel/Novel.mp3", []byte("audiobook bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	firstProber := fakeProber{probe: ffmpeg.Probe{
		Container:  "mp3",
		AudioCodec: "mp3",
		Tags:       map[string]string{"composer": "Tagged Narrator"},
	}}
	parsers := map[string]FilenameParser{audiobook.PluginID: audiobook.New()}
	result, err := Scan(context.Background(), st, firstProber, nil, parsers, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("first Scan returned error: %v", err)
	}
	mediaID := result.Files[0].MediaID
	if err := st.UpsertMediaFields(context.Background(), mediaID, []store.MediaField{
		{Name: "narrator", Type: store.EntityFieldText, Text: "Curated Narrator"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed audiobook bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	secondProber := fakeProber{probe: ffmpeg.Probe{
		Container:  "mp3",
		AudioCodec: "mp3",
		Tags:       map[string]string{"artist": "Tagged Again"},
	}}
	if _, err := Scan(context.Background(), st, secondProber, nil, parsers, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	}); err != nil {
		t.Fatalf("second Scan returned error: %v", err)
	}

	fields, err := st.MediaFields(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	got := mediaFieldMap(fields)["narrator"]
	if got.Text != "Curated Narrator" {
		t.Fatalf("narrator field = %+v, want Curated Narrator", got)
	}
}

func TestScanHydratesPhotosEXIFFieldsAlbumLocationAndSources(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Summer 2025/paris.jpg", []byte("\xff\xd8\xff\xe0 jpeg bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Family Photos", false, "photos", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	serverToday := time.Now()
	captured := time.Date(
		serverToday.Year()-1,
		serverToday.Month(),
		serverToday.Day(),
		12, 0, 0, 0,
		time.UTC,
	)
	sortOrder := int(captured.Unix())
	extractor := &fakeLocalExtractor{fields: map[string]any{
		"capture_date":  captured.Format(time.RFC3339),
		"camera_make":   "Fujifilm",
		"camera_model":  "X100V",
		"width":         6240,
		"height":        4160,
		"orientation":   1,
		"place":         "near Paris, France",
		"gps_lat":       48.8566,
		"gps_lon":       2.3522,
		"ignored_field": "ignored",
	}, entities: []LocalEntity{
		{Kind: "album", Title: "Summer 2025", Relation: "contains", SortOrder: &sortOrder},
		{Kind: "location", Title: "near Paris, France", Relation: "located-in", Cardinality: "one"},
	}}

	result, err := ScanWithLocalExtractors(context.Background(), st, nil, nil, nil, nil, nil, map[string][]LocalExtractor{
		"photos": {extractor},
	}, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if got, want := extractor.calls, []string{"image|paris.jpg"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("extractor calls = %v, want %v", got, want)
	}

	fields, err := st.MediaFields(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	gotFields := mediaFieldMap(fields)
	if gotFields["class"].Text != "photo" {
		t.Fatalf("class field = %+v, want photo", gotFields["class"])
	}
	if gotFields["camera-make"].Text != "Fujifilm" || gotFields["camera-model"].Text != "X100V" {
		t.Fatalf("camera fields = make %+v model %+v", gotFields["camera-make"], gotFields["camera-model"])
	}
	if gotFields["width"].Number != 6240 || gotFields["height"].Number != 4160 || gotFields["orientation"].Number != 1 {
		t.Fatalf("numeric fields = width %+v height %+v orientation %+v", gotFields["width"], gotFields["height"], gotFields["orientation"])
	}
	if gotFields["place"].Text != "near Paris, France" {
		t.Fatalf("place field = %+v, want near Paris, France", gotFields["place"])
	}
	if !gotFields["capture-date"].Date.Equal(captured) {
		t.Fatalf("capture-date = %v, want %v", gotFields["capture-date"].Date, captured)
	}
	if _, ok := gotFields["ignored_field"]; ok {
		t.Fatal("unknown EXIF field was persisted")
	}

	albums, err := st.SourceItems(context.Background(), store.LibraryFilter{AllLibraries: true}, store.SourceQuery{Kind: "entity-items", EntityKind: "album"})
	if err != nil {
		t.Fatal(err)
	}
	album := sourceItemByTitle(albums, "Summer 2025")
	if album == nil {
		t.Fatalf("albums source = %#v, want Summer 2025", albums)
	}
	albumMedia, err := st.SourceItems(context.Background(), store.LibraryFilter{AllLibraries: true}, store.SourceQuery{
		Kind: "entity-media", EntityID: store.EntityID(album.ID),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceItemIDs(albumMedia); len(got) != 1 || got[0] != result.Files[0].MediaID {
		t.Fatalf("album media = %v, want %s", got, result.Files[0].MediaID)
	}
	locations, err := st.SourceItems(context.Background(), store.LibraryFilter{AllLibraries: true}, store.SourceQuery{Kind: "entity-items", EntityKind: "location"})
	if err != nil {
		t.Fatal(err)
	}
	if sourceItemByTitle(locations, "near Paris, France") == nil {
		t.Fatalf("locations source = %#v, want near Paris, France", locations)
	}
	onThisDay, err := st.SourceItems(context.Background(), store.LibraryFilter{AllLibraries: true}, store.SourceQuery{Kind: "on-this-day"})
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceItemIDs(onThisDay); len(got) != 1 || got[0] != result.Files[0].MediaID {
		t.Fatalf("on-this-day = %v, want %s", got, result.Files[0].MediaID)
	}
}

func TestScanClassifiesImageByExtension(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "vacation.jpg", []byte("\xff\xd8\xff\xe0 jpeg bytes"))
	st := storetest.Open(t)

	// A prober that, if consulted, would classify the file as audio (no video,
	// an audio codec). Image classification is by extension and runs before the
	// probe, so this must NOT win — the still stays image-class.
	prober := fakeProber{probe: ffmpeg.Probe{Container: "image2", AudioCodec: "pcm_s16le", HasVideo: false}}
	result, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("imported %d files, want 1 (the .jpg)", len(result.Files))
	}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if media.Class != store.MediaClassImage {
		t.Fatalf("media.Class = %q, want %q", media.Class, store.MediaClassImage)
	}
	// A still has no video codec: the scanner must not default it to
	// the video "h264" — that would pollute the stored row and the Library Health
	// dashboard. An empty codec is the correct, validation-accepted value.
	if media.Codec != "" {
		t.Fatalf("media.Codec = %q, want empty for an image-class still", media.Codec)
	}

	// Re-scan the unchanged file: it takes the cheap fast-path, which seeds the
	// h264 video default in its literal. The image guard must clear it there too,
	// or the unconditional codec upsert would re-pollute the stored row.
	if _, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	}); err != nil {
		t.Fatalf("re-scan returned error: %v", err)
	}
	media, err = st.Media(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if media.Codec != "" {
		t.Fatalf("after fast-path re-scan, media.Codec = %q, want empty", media.Codec)
	}
	if media.Class != store.MediaClassImage {
		t.Fatalf("after fast-path re-scan, media.Class = %q, want %q", media.Class, store.MediaClassImage)
	}
}

func TestScanMapsProbeStreamInventory(t *testing.T) {
	probe := ffmpeg.Probe{
		Streams: []ffmpeg.ProbeStream{
			{Type: "video", Codec: "hevc", Profile: "Main 10", Language: "eng", Default: true,
				Level: 153, BitDepth: 10, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", HDR10Plus: true, DoViProfile: 7, DoViBLCompatID: 6},
			{Type: "audio", Codec: "truehd", Language: "eng", Default: true, Channels: 8, ChannelLayout: "7.1"},
			{Type: "subtitle", Codec: "hdmv_pgs_subtitle", Language: "spa", Forced: true, HearingImpaired: true, VisualImpaired: true, Commentary: true},
		},
		FontAttachments: []ffmpeg.FontAttachment{{Filename: "Arial.ttf", MimeType: "application/x-truetype-font"}},
	}

	streams := streamsFromProbe(probe.Streams)
	if len(streams) != 3 {
		t.Fatalf("len(streams) = %d, want 3", len(streams))
	}
	want := store.MediaFileStream{
		Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Language: "eng", Default: true,
		Level: 153, BitDepth: 10, ColorTransfer: "smpte2084", ColorPrimaries: "bt2020", HDR10Plus: true, DoViProfile: 7, DoViBLCompatID: 6,
	}
	if streams[0] != want {
		t.Fatalf("video stream = %+v, want %+v", streams[0], want)
	}
	if streams[1].Index != 1 || streams[1].ChannelLayout != "7.1" || streams[1].Channels != 8 {
		t.Fatalf("audio stream = %+v, want index 1 / 7.1 / 8 channels", streams[1])
	}
	if streams[2].Index != 2 || !streams[2].Forced || !streams[2].HearingImpaired || !streams[2].VisualImpaired || !streams[2].Commentary || streams[2].Type != "subtitle" {
		t.Fatalf("subtitle stream = %+v, want index 2 / forced / hearing-impaired / visual-impaired / commentary / subtitle", streams[2])
	}

	fonts := fontsFromProbe(probe.FontAttachments)
	if len(fonts) != 1 || fonts[0].Filename != "Arial.ttf" || fonts[0].MimeType != "application/x-truetype-font" {
		t.Fatalf("fonts = %+v, want one Arial.ttf", fonts)
	}

	// An empty probe still yields non-nil slices so the store full-replaces and
	// clears stale inventory rather than leaving a stale row.
	if got := streamsFromProbe(nil); got == nil {
		t.Fatal("streamsFromProbe(nil) returned nil, want empty non-nil slice for full-replace")
	}
	if got := fontsFromProbe(nil); got == nil {
		t.Fatal("fontsFromProbe(nil) returned nil, want empty non-nil slice for full-replace")
	}
}

func TestParseSidecarHints(t *testing.T) {
	cases := []struct {
		hint     string
		language string
		forced   bool
		sdh      bool
	}{
		{"", "", false, false},
		{"en", "en", false, false},
		{"en.forced", "en", true, false},
		{"en.sdh", "en", false, true},
		{"forced.en", "en", true, false},
		{"spa.hi", "spa", false, true},
		{"en.cc", "en", false, true},
	}
	for _, tc := range cases {
		lang, forced, sdh := parseSidecarHints(tc.hint)
		if lang != tc.language || forced != tc.forced || sdh != tc.sdh {
			t.Fatalf("parseSidecarHints(%q) = (%q,%v,%v), want (%q,%v,%v)",
				tc.hint, lang, forced, sdh, tc.language, tc.forced, tc.sdh)
		}
	}
}

func TestScanDiscoversSidecarCaptionTracks(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "movie.mkv", []byte("movie bytes"))
	// Siblings: a plain .vtt, a language-tagged forced .srt, and a language+SDH
	// .srt — all the established conventions FEAT-4 discovers.
	writeScanFile(t, root, "movie.vtt", []byte("WEBVTT\n"))
	writeScanFile(t, root, "movie.en.forced.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nforced\n"))
	writeScanFile(t, root, "movie.es.sdh.srt", []byte("1\n00:00:01,000 --> 00:00:02,000\nhola\n"))
	// A sidecar for a different media file (shared prefix, no dot boundary) must
	// not be misattributed.
	writeScanFile(t, root, "movie2.en.srt", []byte("other\n"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	// Only movie.mkv and movie2.* media files import (.srt/.vtt are not in the
	// media extension allowlist), so the sidecars hang off movie.mkv's
	// fingerprint, not their own Media.
	var movieFP string
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "movie.mkv" {
			movieFP = f.ContentFingerprint
		}
	}
	if movieFP == "" {
		t.Fatalf("movie.mkv not imported: %#v", result.Files)
	}
	captions, err := st.MediaFileSidecarCaptions(context.Background(), movieFP)
	if err != nil {
		t.Fatal(err)
	}
	// Ordered by rel_path: movie.en.forced.srt, movie.es.sdh.srt, movie.vtt.
	if len(captions) != 3 {
		t.Fatalf("captions = %#v, want 3", captions)
	}
	want := []store.MediaFileSidecarCaption{
		{RelPath: "movie.en.forced.srt", Kind: store.SidecarKindCaption, Format: "srt", Language: "en", Forced: true},
		{RelPath: "movie.es.sdh.srt", Kind: store.SidecarKindCaption, Format: "srt", Language: "es", SDH: true},
		{RelPath: "movie.vtt", Kind: store.SidecarKindCaption, Format: "vtt"},
	}
	for i, w := range want {
		if captions[i] != w {
			t.Fatalf("caption[%d] = %+v, want %+v", i, captions[i], w)
		}
	}

	// Re-scan refresh (acceptance d): drop the forced .srt, add a new .vtt, and
	// re-scan. The removed sidecar disappears and the new one appears.
	if err := os.Remove(filepath.Join(root, "movie.en.forced.srt")); err != nil {
		t.Fatal(err)
	}
	writeScanFile(t, root, "movie.fr.vtt", []byte("WEBVTT\n"))
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	}); err != nil {
		t.Fatalf("re-scan returned error: %v", err)
	}
	captions, err = st.MediaFileSidecarCaptions(context.Background(), movieFP)
	if err != nil {
		t.Fatal(err)
	}
	relPaths := make([]string, len(captions))
	for i, c := range captions {
		relPaths[i] = c.RelPath
	}
	wantPaths := []string{"movie.es.sdh.srt", "movie.fr.vtt", "movie.vtt"}
	if len(relPaths) != len(wantPaths) {
		t.Fatalf("re-scan captions = %v, want %v", relPaths, wantPaths)
	}
	for i, p := range wantPaths {
		if relPaths[i] != p {
			t.Fatalf("re-scan caption[%d] = %q, want %q (all: %v)", i, relPaths[i], p, relPaths)
		}
	}
}

// TestScanDiscoversTieredSidecarFormats asserts the ADR-0194 §5 discovery-tier
// widening: Tier 1 .ass/.ssa are recorded as caption sidecars, Tier 2 bitmap
// .sup is recorded, and a .idx is recorded only when its .sub payload sibling is
// present (an orphaned .sub is invalid and never recorded).
func TestScanDiscoversTieredSidecarFormats(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "movie.mkv", []byte("movie bytes"))
	writeScanFile(t, root, "movie.en.ass", []byte("[Events]\n"))
	writeScanFile(t, root, "movie.ssa", []byte("[Events]\n"))
	writeScanFile(t, root, "movie.sup", []byte("PGS bytes"))
	// A complete VobSub pair: NEITHER half is recorded. The .sub payload has no
	// byte route, so an advertised .idx would be a phantom track — a caption in
	// the menu that no client can draw.
	writeScanFile(t, root, "movie.de.idx", []byte("idx bytes"))
	writeScanFile(t, root, "movie.de.sub", []byte("vobsub payload"))
	// An orphaned .sub (no matching .idx) is invalid and must not be recorded.
	writeScanFile(t, root, "movie.orphan.sub", []byte("microdvd?"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	var movieFP string
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "movie.mkv" {
			movieFP = f.ContentFingerprint
		}
	}
	if movieFP == "" {
		t.Fatalf("movie.mkv not imported: %#v", result.Files)
	}
	captions, err := st.MediaFileSidecarCaptions(context.Background(), movieFP)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]store.MediaFileSidecarCaption, len(captions))
	for _, c := range captions {
		got[c.RelPath] = c
	}
	want := []store.MediaFileSidecarCaption{
		{RelPath: "movie.en.ass", Kind: store.SidecarKindCaption, Format: "ass", Language: "en"},
		{RelPath: "movie.ssa", Kind: store.SidecarKindCaption, Format: "ssa"},
		{RelPath: "movie.sup", Kind: store.SidecarKindCaption, Format: "sup"},
	}
	for _, w := range want {
		if got[w.RelPath] != w {
			t.Errorf("caption %q = %+v, want %+v", w.RelPath, got[w.RelPath], w)
		}
	}
	if _, ok := got["movie.orphan.sub"]; ok {
		t.Errorf("orphaned .sub was recorded: %+v", got["movie.orphan.sub"])
	}
	if _, ok := got["movie.de.sub"]; ok {
		t.Errorf("paired .sub payload was recorded as its own caption: %+v", got["movie.de.sub"])
	}
	if _, ok := got["movie.de.idx"]; ok {
		t.Errorf("VobSub .idx was advertised while its .sub payload has no byte route: %+v", got["movie.de.idx"])
	}
	// Every recorded caption carries a non-empty format. The serve path routes on
	// this value and the ingest boundary rejects the empty one, so a row that
	// took the column's '' default would 503 on playback — this asserts the
	// writer never produces one.
	for _, c := range captions {
		if c.Kind == store.SidecarKindCaption && c.Format == "" {
			t.Errorf("caption %q recorded with an empty format", c.RelPath)
		}
	}
	if len(captions) != len(want) {
		t.Fatalf("captions = %#v, want exactly %d", captions, len(want))
	}
}

// TestScanDiscoversInteractiveSidecarTracks asserts the interactive sidecar
// path: a sibling whose extension is in the plugin-declared
// interactive-sidecar set is recorded as a sidecar of the interactive kind
// (raw bytes, no caption hints), alongside a caption sidecar on the same file —
// proving the generic mechanic carries both kinds. The extension here ("toy")
// is a generic test stand-in: scan never names a domain extension, it reads the
// set passed in (in production from plugin.Registry.InteractiveSidecarExts).
func TestScanDiscoversInteractiveSidecarTracks(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	writeScanFile(t, root, "movie.mkv", []byte("movie bytes"))
	writeScanFile(t, root, "movie.vtt", []byte("WEBVTT\n"))
	writeScanFile(t, root, "movie.toy", []byte("interactive bytes, not WebVTT"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, []string{"toy"}, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	var movieFP string
	for _, f := range result.Files {
		if filepath.Base(f.Path) == "movie.mkv" {
			movieFP = f.ContentFingerprint
		}
	}
	if movieFP == "" {
		t.Fatalf("movie.mkv not imported: %#v", result.Files)
	}
	sidecars, err := st.MediaFileSidecarCaptions(context.Background(), movieFP)
	if err != nil {
		t.Fatal(err)
	}
	// Ordered by rel_path: movie.toy (interactive), movie.vtt (caption).
	want := []store.MediaFileSidecarCaption{
		{RelPath: "movie.toy", Kind: store.SidecarKindInteractive, Format: "toy"},
		{RelPath: "movie.vtt", Kind: store.SidecarKindCaption, Format: "vtt"},
	}
	if len(sidecars) != len(want) {
		t.Fatalf("sidecars = %#v, want %#v", sidecars, want)
	}
	for i, w := range want {
		if sidecars[i] != w {
			t.Fatalf("sidecar[%d] = %+v, want %+v", i, sidecars[i], w)
		}
	}

	// Without the extension in the recognised set, the same file is NOT recorded
	// as a sidecar — recognition is gated entirely on the plugin-declared set.
	writeScanFile(t, root, "movie.mkv", []byte("movie bytes"))
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	}); err != nil {
		t.Fatalf("re-scan returned error: %v", err)
	}
	sidecars, err = st.MediaFileSidecarCaptions(context.Background(), movieFP)
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range sidecars {
		if sc.Kind == store.SidecarKindInteractive {
			t.Fatalf("interactive sidecar recorded with no recognised extensions: %+v", sc)
		}
	}
}

func TestScanFallsBackWhenProbeFails(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "clip.mp4", []byte("not really video"))
	st := storetest.Open(t)

	prober := fakeProber{err: errors.New("ffprobe is not available")}
	result, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if media.Container != "mp4" || media.Codec != "h264" {
		t.Fatalf("fallback media = container %q codec %q, want mp4/h264", media.Container, media.Codec)
	}
}

// hangingProber blocks until its context is cancelled, simulating a stuck ffprobe.
type hangingProber struct{}

func (hangingProber) Probe(ctx context.Context, _ string) (ffmpeg.Probe, error) {
	<-ctx.Done()
	return ffmpeg.Probe{}, ctx.Err()
}

func TestScanProbeTimeoutDoesNotHangScan(t *testing.T) {
	// MEDIA-2: a per-file probe that never returns must be cancelled by
	// probeTimeout so the scan job completes rather than stalling forever.
	// Tighten probeTimeout to 50 ms for the test so the per-file cancel fires
	// while the scan context (5 s) stays alive — proving the bound comes from
	// probeTimeout, not a context cancellation from the caller.
	prev := probeTimeout
	probeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { probeTimeout = prev })

	root := t.TempDir()
	writeScanFile(t, root, "clip.mp4", []byte("not really video"))
	st := storetest.Open(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := Scan(ctx, st, hangingProber{}, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 imported file, got %d", len(result.Files))
	}
}

type stubParser struct {
	parsed ParsedMedia
	ok     bool
}

type contextualRefusalParser struct{}

func (contextualRefusalParser) Parse(string, ParseProbe) (ParsedMedia, bool) {
	return ParsedMedia{Title: "Grouped Item", Class: "collection"}, true
}

func (p contextualRefusalParser) ParseWithContext(path string, probe ParseProbe, context ParseContext) (ParsedMedia, bool) {
	if context.DirectoryMediaFileCount == DirectoryMediaFileCountMultiple {
		return ParsedMedia{}, false
	}
	return p.Parse(path, probe)
}

type mixedContextualParser struct{}

func (mixedContextualParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	parsed := ParsedMedia{Title: filepath.Base(filepath.Dir(path)), Class: "collection"}
	if filepath.Base(filepath.Dir(path)) == "parts" {
		parsed.PartGroup = filepath.Dir(path)
		parsed.Index = 1
	}
	return parsed, true
}

func (p mixedContextualParser) ParseWithContext(path string, probe ParseProbe, _ ParseContext) (ParsedMedia, bool) {
	if strings.HasPrefix(filepath.Base(path), "02") {
		return ParsedMedia{}, false
	}
	return p.Parse(path, probe)
}

type taggedContextualParser struct{}

func (taggedContextualParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	return ParsedMedia{Title: filepath.Base(filepath.Dir(path)), Class: "collection"}, true
}

func (p taggedContextualParser) ParseWithContext(path string, probe ParseProbe, _ ParseContext) (ParsedMedia, bool) {
	if probe.Tags["group"] != "valid" {
		return ParsedMedia{}, false
	}
	return p.Parse(path, probe)
}

func TestScanQueuesContextualMultiFileRefusalWithoutImport(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ambiguous")
	writeScanFile(t, dir, "01.mkv", []byte("first"))
	writeScanFile(t, dir, "02.mp4", []byte("second"))
	writeScanFile(t, dir, "notes.txt", []byte("not media"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Contextual", false, "contextual", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	parsers := map[string]FilenameParser{"contextual": contextualRefusalParser{}}

	for pass := 0; pass < 2; pass++ {
		result, scanErr := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
		if scanErr != nil {
			t.Fatalf("Scan pass %d: %v", pass+1, scanErr)
		}
		if result.FilesSeen != 2 || result.MediaImported != 0 || len(result.Files) != 0 {
			t.Fatalf("Scan pass %d result = %#v, want two refused files and no imports", pass+1, result)
		}
	}
	files, err := st.MediaFilesForLibrary(context.Background(), library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("imported files = %#v, want none", files)
	}
	records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("unmatched records = %#v, want one deduplicated folder record", records)
	}
	record := records[0]
	if record.Origin != store.UnmatchedOriginScan || record.ImporterID != "contextual" || record.SourceHandle != dir || record.CandidateTitle != "Grouped Item" || record.FailureReason != store.UnmatchedReasonAmbiguous || record.MediaID != "" {
		t.Fatalf("unmatched record = %#v", record)
	}
}

func TestScanMixedContextualRefusalImportsNoDirectorySiblings(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"generic", "parts"} {
		writeScanFile(t, filepath.Join(root, dir), "01.mp4", []byte(dir+" first"))
		writeScanFile(t, filepath.Join(root, dir), "02.mp4", []byte(dir+" second"))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Contextual", false, "contextual", "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	result, err := Scan(context.Background(), st, nil, nil, map[string]FilenameParser{"contextual": mixedContextualParser{}}, nil, nil, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesSeen != 4 || result.MediaImported != 0 || len(result.Files) != 0 {
		t.Fatalf("result = %#v, want four refused files and no imports", result)
	}
	files, err := st.MediaFilesForLibrary(context.Background(), library.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("mixed refusal imported siblings = %#v", files)
	}
}

func TestScanFastPathContextualParserRetainsProbeTagsAndLastSeen(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "group/01.mp4", []byte("first"))
	writeScanFile(t, root, "group/02.mp4", []byte("second"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Contextual", false, "contextual", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	prober := fakeProber{probe: ffmpeg.Probe{HasVideo: true, Tags: map[string]string{"group": "valid"}}}
	parsers := map[string]FilenameParser{"contextual": taggedContextualParser{}}

	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatal(err)
	}
	before, err := st.MediaFilesForLibrary(context.Background(), library.ID)
	if err != nil || len(before) != 2 {
		t.Fatalf("first scan files = %#v, err = %v", before, err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatal(err)
	}
	after, err := st.MediaFilesForLibrary(context.Background(), library.ID)
	if err != nil || len(after) != 2 {
		t.Fatalf("fast rescan files = %#v, err = %v", after, err)
	}
	for i := range after {
		if !after[i].LastSeenAt.After(before[i].LastSeenAt) {
			t.Fatalf("file %q last_seen = %v, want after %v", after[i].Path, after[i].LastSeenAt, before[i].LastSeenAt)
		}
	}
}

func (p stubParser) Parse(string, ParseProbe) (ParsedMedia, bool) {
	return p.parsed, p.ok
}

type ordinalParser struct{}

func (ordinalParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	base := filepath.Base(path)
	var index int
	if _, err := fmt.Sscanf(base, "%d", &index); err != nil || index <= 0 {
		return ParsedMedia{}, false
	}
	return ParsedMedia{Title: filepath.Base(filepath.Dir(path)), Class: audiobook.ClassAudiobook, Index: index, PartGroup: filepath.Clean(filepath.Dir(path))}, true
}

func (ordinalParser) PartMarkerTitle(path string, _ ParseProbe) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if _, title, ok := strings.Cut(stem, " - "); ok {
		return title
	}
	return stem
}

type familyArchiveParser struct{}

func (familyArchiveParser) Parse(string, ParseProbe) (ParsedMedia, bool) {
	return ParsedMedia{Title: "First Steps", Class: "home-video", Year: 2017}, true
}

func (familyArchiveParser) ParseTypedMediaFields(_ string, _ ParseProbe) []ParsedField {
	return []ParsedField{
		{Name: "source-folder", Text: "Summer Picnic"},
		{Name: "sidecar-title", Text: "First Steps"},
		{Name: "sidecar-summary", Text: "Walking across the kitchen"},
		{Name: "camera-make", Text: "Canon"},
		{Name: "camera-model", Text: "VIXIA HF G20"},
		{Name: "capture-date", Date: time.Date(2017, 8, 19, 0, 0, 0, 0, time.UTC)},
	}
}

type recordingIdentifier struct {
	mediaID     store.MediaID
	pluginID    string
	fingerprint string
	parsed      ParsedMedia
	calls       int
}

func (r *recordingIdentifier) AutoIdentify(_ context.Context, mediaID store.MediaID, pluginID, fingerprint string, parsed ParsedMedia) error {
	r.mediaID = mediaID
	r.pluginID = pluginID
	r.fingerprint = fingerprint
	r.parsed = parsed
	r.calls++
	return nil
}

func TestScanAppliesParserClassForBoundLibrary(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Show.S02E05.mkv", []byte("episode bytes"))
	st := storetest.Open(t)

	// A Library bound to the "filmtv" plugin resolves the matching parser.
	library, err := st.CreateLibrary(context.Background(), "Shows", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	parsers := map[string]FilenameParser{
		"filmtv": stubParser{parsed: ParsedMedia{Title: "Show", Class: "episode", Year: 1999}, ok: true},
	}
	result, err := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(result.Files))
	}

	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true}
	fields, err := st.MediaFields(context.Background(), result.Files[0].MediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields[0].Name != "class" || fields[0].Text != "episode" {
		t.Fatalf("class field = %#v, want episode", fields)
	}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "Show" || media.Year != 1999 {
		t.Fatalf("media = title %q year %d, want Show/1999", media.Title, media.Year)
	}
}

// Mutation check (2026-08-21): disabling appendParsedStructuralMediaFields failed with
// "scan_test.go:1395: episode class = store.MediaField{Name:\"\", Type:\"\", Text:\"\", Number:0, Flag:false, Date:time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)}".
//
// Evidence — SCAN-PERSIST-PARSED-SEASON-EPISODE-1 (2026-08-22):
// command: go test ./internal/scan/... -race
// head: cabf1f8094af917ef97849e941219e7da54bf764 (plus this working-tree diff)
// UTC start: 2026-08-22T00:51:34Z
// UTC end: 2026-08-22T00:51:35Z
// elapsed: 1s (cached confirmation; preceding uncached run reported 256.778s)
// exit code: 0
// log: .agent-evidence/SCAN-PERSIST-PARSED-SEASON-EPISODE-1.log
func TestScanPersistsParsedStructuralFieldsOffline(t *testing.T) {
	root := t.TempDir()
	episodePath := writeScanFile(t, root, "Show.S01E03.Title.1080p.mkv", []byte("episode"))
	rangePath := writeScanFile(t, root, "Show.S01E03-E04.1080p.mkv", []byte("range"))
	filmPath := writeScanFile(t, root, "Film.1999.1080p.mkv", []byte("film"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Film TV", false, filmtv.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	parsers := map[string]FilenameParser{filmtv.PluginID: filmtv.New()}

	result, err := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	mediaByPath := map[string]store.MediaID{}
	for _, file := range result.Files {
		mediaByPath[file.Path] = file.MediaID
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true}
	fieldsFor := func(path string) map[string]store.MediaField {
		t.Helper()
		fields, fieldErr := st.MediaFields(context.Background(), mediaByPath[path], filter)
		if fieldErr != nil {
			t.Fatalf("MediaFields(%s): %v", filepath.Base(path), fieldErr)
		}
		byName := make(map[string]store.MediaField, len(fields))
		for _, field := range fields {
			byName[field.Name] = field
		}
		return byName
	}
	assertNumber := func(fields map[string]store.MediaField, name string, want float64) {
		t.Helper()
		field, ok := fields[name]
		if !ok || field.Type != store.EntityFieldNumber || field.Number != want {
			t.Fatalf("%s = %#v, want number %.0f", name, field, want)
		}
	}

	episodeFields := fieldsFor(episodePath)
	if field := episodeFields["class"]; field.Type != store.EntityFieldText || field.Text != "episode" {
		t.Fatalf("episode class = %#v", field)
	}
	assertNumber(episodeFields, "sequence", 1)
	assertNumber(episodeFields, "index", 3)
	if _, ok := episodeFields["index-end"]; ok {
		t.Fatalf("single item unexpectedly has index-end: %#v", episodeFields["index-end"])
	}
	rangeFields := fieldsFor(rangePath)
	assertNumber(rangeFields, "sequence", 1)
	assertNumber(rangeFields, "index", 3)
	assertNumber(rangeFields, "index-end", 4)
	filmFields := fieldsFor(filmPath)
	if field := filmFields["class"]; field.Type != store.EntityFieldText || field.Text != "movie" {
		t.Fatalf("film class = %#v", field)
	}
	for _, name := range []string{"sequence", "index", "index-end"} {
		if _, ok := filmFields[name]; ok {
			t.Fatalf("film unexpectedly has %s: %#v", name, filmFields[name])
		}
	}
	film, err := st.Media(context.Background(), mediaByPath[filmPath], filter)
	if err != nil {
		t.Fatalf("Media(film): %v", err)
	}
	if film.Year != 1999 {
		t.Fatalf("film year = %d, want 1999", film.Year)
	}

	assertProvenance := func(mediaID store.MediaID, field string, want store.MutationSource) {
		t.Helper()
		got, provenanceErr := st.MediaFieldProvenance(context.Background(), mediaID, field)
		if provenanceErr != nil {
			t.Fatalf("MediaFieldProvenance(%s): %v", field, provenanceErr)
		}
		if got != want {
			t.Fatalf("provenance(%s) = %q, want %q", field, got, want)
		}
	}
	assertSingleFieldRow := func(mediaID store.MediaID, field string) {
		t.Helper()
		fields, fieldsErr := st.MediaFields(context.Background(), mediaID, filter)
		if fieldsErr != nil {
			t.Fatalf("MediaFields(%s): %v", field, fieldsErr)
		}
		rows := 0
		for _, stored := range fields {
			if stored.Name == field {
				rows++
			}
		}
		if rows != 1 {
			t.Fatalf("%s rows = %d, want 1", field, rows)
		}
	}
	for _, field := range []string{"class", "sequence", "index"} {
		assertProvenance(mediaByPath[episodePath], field, store.MutationSourceScan)
	}
	assertProvenance(mediaByPath[rangePath], "index-end", store.MutationSourceScan)
	assertProvenance(mediaByPath[filmPath], "year", store.MutationSourceScan)

	identified := []store.MediaField{
		{Name: "sequence", Type: store.EntityFieldNumber, Number: 2},
		{Name: "index", Type: store.EntityFieldNumber, Number: 7},
		{Name: "index-end", Type: store.EntityFieldNumber, Number: 8},
	}
	if err := st.UpsertMediaFieldsWithSource(context.Background(), mediaByPath[rangePath], identified, store.MutationSourceOperator); err != nil {
		t.Fatalf("identify fields: %v", err)
	}
	identifiedFields := fieldsFor(rangePath)
	assertNumber(identifiedFields, "sequence", 2)
	assertNumber(identifiedFields, "index", 7)
	assertNumber(identifiedFields, "index-end", 8)
	for _, field := range []string{"sequence", "index", "index-end"} {
		assertProvenance(mediaByPath[rangePath], field, store.MutationSourceOperator)
		assertSingleFieldRow(mediaByPath[rangePath], field)
	}
	identifiedYear := 2001
	if err := st.UpdateMediaMetadata(context.Background(), mediaByPath[filmPath], store.MediaMetadata{Year: &identifiedYear, Source: store.MutationSourceOperator}); err != nil {
		t.Fatalf("identify year: %v", err)
	}
	film, err = st.Media(context.Background(), mediaByPath[filmPath], filter)
	if err != nil {
		t.Fatalf("Media(identified film): %v", err)
	}
	if film.Year != identifiedYear {
		t.Fatalf("identified film year = %d, want %d", film.Year, identifiedYear)
	}
	assertProvenance(mediaByPath[filmPath], "year", store.MutationSourceOperator)

	beforeRescan := fieldsFor(rangePath)
	if _, err := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatalf("unchanged rescan: %v", err)
	}
	afterRescan := fieldsFor(rangePath)
	if !reflect.DeepEqual(afterRescan, beforeRescan) {
		t.Fatalf("unchanged rescan churned fields: before=%#v after=%#v", beforeRescan, afterRescan)
	}
	afterRescanRows, err := st.MediaFields(context.Background(), mediaByPath[rangePath], filter)
	if err != nil {
		t.Fatalf("MediaFields(after rescan): %v", err)
	}
	rowCounts := make(map[string]int, len(afterRescanRows))
	for _, field := range afterRescanRows {
		rowCounts[field.Name]++
	}
	for _, field := range []string{"sequence", "index", "index-end"} {
		if rowCounts[field] != 1 {
			t.Fatalf("%s rows after unchanged rescan = %d, want 1", field, rowCounts[field])
		}
		assertProvenance(mediaByPath[rangePath], field, store.MutationSourceOperator)
	}
	film, err = st.Media(context.Background(), mediaByPath[filmPath], filter)
	if err != nil || film.Year != identifiedYear {
		t.Fatalf("rescan film = year %d error %v, want identified year %d", film.Year, err, identifiedYear)
	}
	assertProvenance(mediaByPath[filmPath], "year", store.MutationSourceOperator)
}

func TestScanFamilyArchiveLibraryPersistsLocalHintsAndReachesManualQueuePath(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Summer Picnic/clip01.mov", []byte("family archive bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Family Archive", false, "familyarchive", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	identifier := &recordingIdentifier{}

	result, err := Scan(context.Background(), st, nil, nil, map[string]FilenameParser{"familyarchive": familyArchiveParser{}}, nil, identifier, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(result.Files))
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}, Revealed: true}
	media, err := st.Media(context.Background(), result.Files[0].MediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.Title != "First Steps" || media.Year != 2017 {
		t.Fatalf("media = title %q year %d, want First Steps/2017", media.Title, media.Year)
	}
	fields, err := st.MediaFields(context.Background(), media.ID, filter)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.MediaField{}
	for _, field := range fields {
		got[field.Name] = field
	}
	for name, text := range map[string]string{
		"class":           "home-video",
		"source-folder":   "Summer Picnic",
		"sidecar-title":   "First Steps",
		"sidecar-summary": "Walking across the kitchen",
		"camera-make":     "Canon",
		"camera-model":    "VIXIA HF G20",
	} {
		if got[name].Text != text {
			t.Fatalf("%s field = %#v, want text %q", name, got[name], text)
		}
	}
	if field := got["capture-date"]; field.Type != store.EntityFieldDate || field.Date.Format("2006-01-02") != "2017-08-19" {
		t.Fatalf("capture-date field = %#v, want date 2017-08-19", field)
	}
	if identifier.calls != 1 || identifier.pluginID != "familyarchive" || identifier.mediaID != media.ID {
		t.Fatalf("identifier = calls:%d plugin:%q media:%q", identifier.calls, identifier.pluginID, identifier.mediaID)
	}
	if identifier.parsed.Title != "First Steps" || identifier.parsed.Class != "home-video" {
		t.Fatalf("identifier parsed = %+v, want First Steps/home-video", identifier.parsed)
	}
}

func TestScanFoldsParserIndexedLeafFolderIntoOrderedParts(t *testing.T) {
	root := t.TempDir()
	p1 := writeScanFile(t, root, "Author/Series 01/Book/1 - Opening.mp3", []byte("part one"))
	p10 := writeScanFile(t, root, "Author/Series 01/Book/10 - Ending.mp3", []byte("part ten"))
	p2 := writeScanFile(t, root, "Author/Series 01/Book/2 - Middle.mp3", []byte("part two"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	result, err := Scan(context.Background(), st, nil, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.FilesSeen != 3 {
		t.Fatalf("FilesSeen = %d, want 3", result.FilesSeen)
	}
	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1 grouped Media", result.MediaImported)
	}

	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Book" {
		t.Fatalf("source items = %#v, want one Book Media", items)
	}
	mediaID := items[0].ID
	files, err := st.MediaFilesForMedia(context.Background(), items[0].ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3 Parts", len(files))
	}
	wantPaths := []string{p1, p2, p10}
	for i, file := range files {
		if file.PartOrdinal != i || file.Path != wantPaths[i] || file.MediaID != items[0].ID {
			t.Fatalf("files[%d] = {media:%q path:%q part:%d}, want media %q path %q part %d", i, file.MediaID, file.Path, file.PartOrdinal, items[0].ID, wantPaths[i], i)
		}
	}

	result, err = Scan(context.Background(), st, nil, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("second Scan returned error: %v", err)
	}
	if result.FilesSeen != 3 || result.MediaImported != 1 {
		t.Fatalf("second result = files %d media %d, want 3 files / 1 Media", result.FilesSeen, result.MediaImported)
	}
	files, err = st.MediaFilesForMedia(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("second scan files = %d, want 3 Parts", len(files))
	}
	for i, file := range files {
		if file.PartOrdinal != i || file.Path != wantPaths[i] || file.MediaID != mediaID {
			t.Fatalf("second files[%d] = {media:%q path:%q part:%d}, want media %q path %q part %d", i, file.MediaID, file.Path, file.PartOrdinal, mediaID, wantPaths[i], i)
		}
	}
}

func TestScanSynthesizesChapterMarkersAtPartBoundaries(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Author/Book/1 - Opening.mp3", []byte("part one"))
	writeScanFile(t, root, "Author/Book/2 - Middle.mp3", []byte("part two"))
	writeScanFile(t, root, "Author/Book/3 - Ending.mp3", []byte("part three"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}

	prober := &countingProber{probe: ffmpeg.Probe{Container: "mp3", AudioCodec: "mp3", DurationSeconds: 60}}
	result, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if prober.calls != 3 {
		t.Fatalf("probe calls = %d, want 3", prober.calls)
	}
	mediaID := result.Files[0].MediaID
	markers, err := st.MarkersForMedia(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 3 {
		t.Fatalf("markers = %+v, want one per Part", markers)
	}
	wantTitles := []string{"Opening", "Middle", "Ending"}
	for i, marker := range markers {
		wantStart := float64(i * 60)
		wantEnd := float64((i + 1) * 60)
		if marker.Title != wantTitles[i] || marker.StartSeconds != wantStart || marker.EndSeconds == nil || *marker.EndSeconds != wantEnd {
			t.Fatalf("marker %d = %+v, want %q %.0f-%.0f", i, marker, wantTitles[i], wantStart, wantEnd)
		}
		if marker.Provenance != store.ProvenanceDetected || marker.Method != partBoundaryMarkerMethod || marker.Confidence != 1 {
			t.Fatalf("marker %d provenance = %+v, want detected %s confidence 1", i, marker, partBoundaryMarkerMethod)
		}
	}

	ids := []store.MarkerID{markers[0].ID, markers[1].ID, markers[2].ID}
	noProbe := &countingProber{}
	if _, err := Scan(context.Background(), st, noProbe, nil, map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	}); err != nil {
		t.Fatalf("second Scan returned error: %v", err)
	}
	if noProbe.calls != 0 {
		t.Fatalf("second scan probe calls = %d, want 0", noProbe.calls)
	}
	markers, err = st.MarkersForMedia(context.Background(), mediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for i, marker := range markers {
		if marker.ID != ids[i] {
			t.Fatalf("marker %d ID = %q, want stable %q", i, marker.ID, ids[i])
		}
	}
}

func TestScanRecoversMissingPartBoundaryMarkersOnFastPath(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Author/Book/1 - Opening.mp3", []byte("part one"))
	writeScanFile(t, root, "Author/Book/2 - Ending.mp3", []byte("part two"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Audiobooks", false, audiobook.PluginID, "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	parsers := map[string]FilenameParser{audiobook.PluginID: ordinalParser{}}

	first, err := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("probe-less Scan returned error: %v", err)
	}
	markers, err := st.MarkersForMedia(context.Background(), first.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 0 {
		t.Fatalf("probe-less markers = %+v, want none without durations", markers)
	}

	prober := &countingProber{probe: ffmpeg.Probe{Container: "mp3", AudioCodec: "mp3", DurationSeconds: 45}}
	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatalf("recovery Scan returned error: %v", err)
	}
	if prober.calls != 2 {
		t.Fatalf("recovery probe calls = %d, want 2 missing Part durations", prober.calls)
	}
	markers, err = st.MarkersForMedia(context.Background(), first.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 2 || markers[0].StartSeconds != 0 || markers[0].EndSeconds == nil || *markers[0].EndSeconds != 45 || markers[1].StartSeconds != 45 || markers[1].EndSeconds == nil || *markers[1].EndSeconds != 90 {
		t.Fatalf("recovered markers = %+v, want 0-45 and 45-90", markers)
	}
}

func TestScanDoesNotFoldMultiTrackFolderWithoutParserOrdinals(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Album/01 Track.mp3", []byte("track one"))
	writeScanFile(t, root, "Album/02 Track.mp3", []byte("track two"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: library.ID,
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.FilesSeen != 2 || result.MediaImported != 2 {
		t.Fatalf("result = files %d media %d, want 2 files / 2 Media", result.FilesSeen, result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{library.ID}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("source items = %d, want 2 separate tracks", len(items))
	}
}

func TestScannedMediaCanDirectPlayAndServeRange(t *testing.T) {
	root := t.TempDir()
	body := []byte("0123456789abcdef")
	writeScanFile(t, root, "clip.mp4", body)
	st := storetest.Open(t)
	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Fatalf("len(Files) = %d, want 1", len(result.Files))
	}

	authService := auth.NewService(st)
	scope, err := authService.Scope(context.Background(), "session-default")
	if err != nil {
		t.Fatal(err)
	}
	tokens := streamtoken.NewService([]byte("test-token-key"), func() time.Time { return time.Unix(0, 0).UTC() })
	playbackInfo := playback.NewService(st, tokens, func() time.Time { return time.Unix(0, 0).UTC() })
	info, err := playbackInfo.Info(context.Background(), scope, playback.InfoRequest{
		MediaID: string(result.Files[0].MediaID),
		CapabilityProfile: playback.CapabilityProfile{
			Client:       "beamfall-test",
			Containers:   []string{"mp4"},
			DirectCodecs: []string{"h264"},
		},
	})
	if err != nil {
		t.Fatalf("playbackInfo returned error: %v", err)
	}
	if info.Decision != "direct-play" {
		t.Fatalf("Decision = %q, want direct-play", info.Decision)
	}

	req := httptest.NewRequest(http.MethodGet, info.Streams[0].URL, nil)
	req.Header.Set("Range", "bytes=2-5")
	rec := httptest.NewRecorder()
	mediahttp.NewHandler(st, authService, tokens, artwork.NewService(st, nil, t.TempDir()), playback.NewSessions(nil, t.TempDir(), nil)).Direct(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("2345")) {
		t.Fatalf("range body = %q, want 2345", rec.Body.String())
	}
}

func TestScanJobHandlerImportsFiles(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "clip.mp4", []byte("clip bytes"))
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	loaded, err := st.Job(context.Background(), "job-scan")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.JobStatusCompleted {
		t.Fatalf("job status = %q error=%q, want completed", loaded.Status, loaded.Error)
	}
	if loaded.Progress.Current != 1 || loaded.Progress.Total != 1 || loaded.Progress.Unit != "files" {
		t.Fatalf("progress = %#v, want 1/1 files", loaded.Progress)
	}
	if !bytes.Contains(loaded.Checkpoint, []byte(`"stage":"complete"`)) {
		t.Fatalf("checkpoint = %s, want complete stage", loaded.Checkpoint)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "clip") {
		t.Fatalf("sourceItems = %#v, want imported clip", items)
	}
}

func TestScanJobHandlerResumesAfterCheckpoint(t *testing.T) {
	root := t.TempDir()
	first := writeScanFile(t, root, "01-first.mp4", []byte("first bytes"))
	writeScanFile(t, root, "02-second.mp4", []byte("second bytes"))
	st := storetest.Open(t)
	fingerprint, err := store.HashMediaFileContent(first)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(context.Background(), store.ImportMediaFile{
		LibraryID:          "library-local",
		Path:               first,
		Title:              "Already Imported",
		Container:          "mp4",
		Codec:              "h264",
		ContentFingerprint: fingerprint,
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan-resume",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	job.Checkpoint = mustMarshalCheckpoint(t, jobCheckpoint{
		Version:       1,
		Stage:         "walk",
		LastPath:      first,
		FilesSeen:     1,
		MediaImported: 1,
	})
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "Already Imported") {
		t.Fatalf("checkpointed file was imported again; sourceItems = %#v", items)
	}
	if !hasSourceItemWithTitle(items, "02-second") {
		t.Fatalf("remaining file was not imported; sourceItems = %#v", items)
	}
	loaded, err := st.Job(context.Background(), "job-scan-resume")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Progress.Current != 2 || loaded.Progress.Total != 2 {
		t.Fatalf("progress = %#v, want 2/2 files", loaded.Progress)
	}
	if !bytes.Contains(loaded.Checkpoint, []byte(`"last_path":"`+filepath.ToSlash(filepath.Clean(filepath.Join(root, "02-second.mp4")))+`"`)) {
		t.Fatalf("checkpoint = %s, want last path for second file", loaded.Checkpoint)
	}
}

func TestScanJobHandlerYieldsCheckpointAndResumes(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 101; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan-yield",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("first RunOne returned error: %v", err)
	}
	yielded, err := st.Job(context.Background(), "job-scan-yield")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("first status = %q error=%q, want yielded", yielded.Status, yielded.Error)
	}
	if yielded.Error != "" || yielded.LeaseOwner != "" {
		t.Fatalf("yielded job retained error/lease: %#v", yielded)
	}
	if yielded.Progress.Current != 100 || yielded.Progress.Unit != "files" {
		t.Fatalf("yielded progress = %#v, want 100 files", yielded.Progress)
	}
	yieldedCheckpoint := mustDecodeCheckpoint(t, yielded.Checkpoint)
	expectedYieldPath := filepath.Clean(filepath.Join(root, "clip-099.mp4"))
	if yieldedCheckpoint.Stage != "scanning" || yieldedCheckpoint.FilesSeen != 100 || yieldedCheckpoint.MediaImported != 100 || yieldedCheckpoint.LastPath != expectedYieldPath {
		t.Fatalf("yielded checkpoint = %#v, want scanning at %q", yieldedCheckpoint, expectedYieldPath)
	}

	if _, err := registry.RunOne(context.Background(), nil, now.Add(time.Second)); err != nil {
		t.Fatalf("second RunOne returned error: %v", err)
	}
	completed, err := st.Job(context.Background(), "job-scan-yield")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("second status = %q error=%q, want completed", completed.Status, completed.Error)
	}
	if completed.Progress.Current != 101 || completed.Progress.Total != 101 {
		t.Fatalf("completed progress = %#v, want 101/101 files", completed.Progress)
	}
	completedCheckpoint := mustDecodeCheckpoint(t, completed.Checkpoint)
	expectedCompletePath := filepath.Clean(filepath.Join(root, "clip-100.mp4"))
	if completedCheckpoint.Stage != "complete" || completedCheckpoint.FilesSeen != 101 || completedCheckpoint.LastPath != expectedCompletePath {
		t.Fatalf("completed checkpoint = %#v, want complete at %q", completedCheckpoint, expectedCompletePath)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "clip-000") || !hasSourceItemWithTitle(items, "clip-100") {
		t.Fatalf("sourceItems missing first/last yielded imports: %#v", items)
	}
}

func TestScanJobLinksExtrasAcrossYieldBoundary(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "000 Feature.mp4", []byte("feature bytes"))
	for i := 0; i < scanJobFileBatchSize-1; i++ {
		writeScanFile(t, root, "Extras/000 filler-"+zeroPad(i)+".mp4", []byte("filler "+zeroPad(i)))
	}
	writeScanFile(t, root, "Extras/Making Of.mp4", []byte("making-of bytes"))
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan-extra-yield",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("first RunOne returned error: %v", err)
	}
	yielded, err := st.Job(context.Background(), "job-scan-extra-yield")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("first status = %q error=%q, want yielded", yielded.Status, yielded.Error)
	}
	if _, err := registry.RunOne(context.Background(), nil, now.Add(time.Second)); err != nil {
		t.Fatalf("second RunOne returned error: %v", err)
	}
	completed, err := st.Job(context.Background(), "job-scan-extra-yield")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("second status = %q error=%q, want completed", completed.Status, completed.Error)
	}

	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(context.Background(), filter, store.SourceQuery{Kind: "recently-added", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	feature := sourceItemByTitle(items, "000 Feature")
	if feature == nil {
		t.Fatalf("feature missing from browse Source: %#v", items)
	}
	fingerprint, err := store.HashMediaFileContent(filepath.Join(root, "Extras", "Making Of.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	makingOfID, ok, err := st.MediaFileExistsByFingerprint(context.Background(), fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("making-of Extra was not imported")
	}
	extras, err := st.MediaRelationsOf(context.Background(), store.MediaID(feature.ID), store.MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(extra-of): %v", err)
	}
	foundMakingOf := false
	for _, extra := range extras {
		if extra.ID == makingOfID {
			foundMakingOf = true
			break
		}
	}
	if !foundMakingOf {
		t.Fatalf("extras of yielded feature = %#v, want making-of %q", extras, makingOfID)
	}
}

// TestScanJobResumeSkipsNoFilesAfterMidEnumerationCrash guards the H14
// invariant: the checkpoint must only ever advance to a file that was already
// durably catalogued, so an interrupt mid-enumeration never causes a resumed
// scan to skip the in-flight file. It simulates a crash by cancelling the run
// context from inside the prober as it reaches the third file — at that point
// files 0 and 1 are imported and their per-file checkpoints persisted, but file
// 2 has not yet been imported. After the crash the durable checkpoint's
// LastPath must point at file 1 (the last completed boundary), not file 2; on
// resume every file, including file 2, must end up catalogued.
func TestScanJobResumeSkipsNoFilesAfterMidEnumerationCrash(t *testing.T) {
	root := t.TempDir()
	const fileCount = 5
	for i := 0; i < fileCount; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	const crashAt = 2
	crashPath := filepath.Clean(filepath.Join(root, "clip-"+zeroPad(crashAt)+".mp4"))
	lastCompletePath := filepath.Clean(filepath.Join(root, "clip-"+zeroPad(crashAt-1)+".mp4"))

	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan-crash",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	// crashCtx is cancelled when the prober reaches clip-002, mimicking a
	// process interruption while that file is mid-processing (after probing,
	// before its own ImportMediaFile). The cancellation propagates through the
	// walk and yields the job with whatever checkpoint the prior files left.
	crashCtx, cancelCrash := context.WithCancel(context.Background())
	defer cancelCrash()
	crashProber := cancelOnPathProber{cancel: cancelCrash, at: crashPath}
	crashRegistry := jobs.NewRegistry(st, "worker")
	crashRegistry.Register(JobKind, Handler(st, crashProber, nil, nil, nil, nil))

	if _, err := crashRegistry.RunOne(crashCtx, nil, now); err != nil {
		t.Fatalf("crash RunOne returned error: %v", err)
	}
	crashed, err := st.Job(context.Background(), "job-scan-crash")
	if err != nil {
		t.Fatal(err)
	}
	if crashed.Status != store.JobStatusYielded {
		t.Fatalf("crashed status = %q error=%q, want yielded", crashed.Status, crashed.Error)
	}
	crashedCheckpoint := mustDecodeCheckpoint(t, crashed.Checkpoint)
	// The cursor must sit at the last fully-imported file, never at the file the
	// crash interrupted — otherwise resume would skip clip-002 forever (H14).
	if crashedCheckpoint.LastPath != lastCompletePath {
		t.Fatalf("crashed checkpoint LastPath = %q, want last completed %q (cursor advanced past an unprocessed file)", crashedCheckpoint.LastPath, lastCompletePath)
	}

	// Resume cleanly (no crash prober) and drive to completion. The cancel
	// path sets RunAfter to wall-clock time, so resume from there onward.
	resumeBase := crashed.RunAfter
	if resumeBase.IsZero() {
		resumeBase = now
	}
	resumeRegistry := jobs.NewRegistry(st, "worker")
	resumeRegistry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))
	for attempt := 0; attempt < fileCount+2; attempt++ {
		if _, err := resumeRegistry.RunOne(context.Background(), nil, resumeBase.Add(time.Duration(attempt+1)*time.Second)); err != nil {
			t.Fatalf("resume RunOne attempt %d returned error: %v", attempt, err)
		}
		loaded, err := st.Job(context.Background(), "job-scan-crash")
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == store.JobStatusCompleted {
			break
		}
		if loaded.Status != store.JobStatusYielded {
			t.Fatalf("resume attempt %d status = %q error=%q, want yielded or completed", attempt, loaded.Status, loaded.Error)
		}
	}
	completed, err := st.Job(context.Background(), "job-scan-crash")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("final status = %q error=%q, want completed", completed.Status, completed.Error)
	}

	// The H14 assertion: every file is catalogued, none skipped by resume.
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < fileCount; i++ {
		title := "clip-" + zeroPad(i)
		if !hasSourceItemWithTitle(items, title) {
			t.Fatalf("file %q skipped by resume; sourceItems = %#v", title, items)
		}
	}
}

// cancelOnPathProber cancels the scan context the first time it is asked to
// probe a target path, so a test can interrupt a job at a deterministic file.
type cancelOnPathProber struct {
	cancel context.CancelFunc
	at     string
}

func (p cancelOnPathProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	if filepath.Clean(path) == p.at {
		p.cancel()
	}
	return ffmpeg.Probe{}, errors.New("probe unavailable in test")
}

func TestNewJobUsesStablePayloadAndNormalizedRoot(t *testing.T) {
	root := t.TempDir()
	dirtyRoot := filepath.Join(root, "nested", "..", ".")
	now := time.Unix(100, 0).UTC()

	job, err := NewJob(JobRequest{
		ID:        "job-scan",
		LibraryID: "library-local",
		Root:      dirtyRoot,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}

	expectedRoot := filepath.Clean(root)
	if job.DedupeKey != "library-local:"+expectedRoot {
		t.Fatalf("DedupeKey = %q, want normalized root %q", job.DedupeKey, expectedRoot)
	}

	var payload map[string]any
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["version"] != float64(2) {
		t.Fatalf("version = %#v, want 2", payload["version"])
	}
	if payload["library_id"] != "library-local" {
		t.Fatalf("library_id = %#v, want library-local", payload["library_id"])
	}
	if payload["root"] != expectedRoot {
		t.Fatalf("root = %#v, want %q", payload["root"], expectedRoot)
	}
	// A normal (non-backfill) job carries backfill=false.
	if payload["backfill"] != false {
		t.Fatalf("backfill = %#v, want false", payload["backfill"])
	}
	if _, ok := payload["LibraryID"]; ok {
		t.Fatalf("payload used Go field name LibraryID: %s", job.Payload)
	}
	if _, ok := payload["Root"]; ok {
		t.Fatalf("payload used Go field name Root: %s", job.Payload)
	}
}

func mustMarshalCheckpoint(t *testing.T, checkpoint jobCheckpoint) []byte {
	t.Helper()
	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustDecodeCheckpoint(t *testing.T, data []byte) jobCheckpoint {
	t.Helper()
	var checkpoint jobCheckpoint
	if err := json.Unmarshal(data, &checkpoint); err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func writeScanFile(t *testing.T, root string, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Backdate the mtime past the file-stability window so the
	// scan treats the file as settled and imports it. Freshly written files
	// are otherwise skipped as still-streaming; tests that exercise the
	// stability gate itself set their own mtimes rather than use this helper.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasSourceItemWithTitle(items []store.SourceItem, title string) bool {
	for _, item := range items {
		if item.Title == title {
			return true
		}
	}
	return false
}

func duplicatePathIndex(files []ImportedFile, path string) int {
	for i, file := range files {
		if file.Path == path {
			return i
		}
	}
	return 0
}

func zeroPad(value int) string {
	if value < 10 {
		return "00" + string(rune('0'+value))
	}
	if value < 100 {
		return "0" + string(rune('0'+value/10)) + string(rune('0'+value%10))
	}
	return string(rune('0'+value/100)) + string(rune('0'+value/10%10)) + string(rune('0'+value%10))
}

func TestScanStoresFingerprintHashes(t *testing.T) {
	root := t.TempDir()
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(i)
	}
	writeScanFile(t, root, "clip.mkv", data)
	st := storetest.Open(t)

	prober := fakeProber{probe: ffmpeg.Probe{Container: "matroska", VideoCodec: "hevc", HasVideo: true, DurationSeconds: 120}}
	grabber := &jpegGrabber{img: gradientImage()}
	result, err := Scan(context.Background(), st, prober, grabber, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.OSHash != "2c2824201c181420" {
		t.Fatalf("OSHash = %q, want 2c2824201c181420", file.OSHash)
	}
	if file.PHash != "ffffffffffffffff" {
		t.Fatalf("PHash = %q, want ffffffffffffffff", file.PHash)
	}
}

func TestScanWithoutGrabberStillStoresOSHash(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "clip.mp4", []byte{0xFF, 0x01, 0x02})
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	file, err := st.PrimaryMediaFile(context.Background(), result.Files[0].MediaID, store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}})
	if err != nil {
		t.Fatalf("PrimaryMediaFile returned error: %v", err)
	}
	if file.OSHash != "0000000000040401" {
		t.Fatalf("OSHash = %q, want 0000000000040401", file.OSHash)
	}
	if file.PHash != "" {
		t.Fatalf("PHash = %q, want empty without a frame grabber", file.PHash)
	}
}

// countingProber counts Probe calls so a test can assert the scan fast-path
// performs zero probes on an unchanged library.
type countingProber struct {
	probe ffmpeg.Probe
	calls int
}

func (p *countingProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	p.calls++
	return p.probe, nil
}

type pathProber struct {
	probes map[string]ffmpeg.Probe
	calls  []string
}

func (p *pathProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	p.calls = append(p.calls, path)
	return p.probes[path], nil
}

// countHashes swaps the package-level hash seam for a counter, restored at test
// end. It returns a pointer the test reads after each scan.
func countHashes(t *testing.T) *int {
	t.Helper()
	calls := 0
	original := hashFileContent
	hashFileContent = func(path string) (string, error) {
		calls++
		return original(path)
	}
	t.Cleanup(func() { hashFileContent = original })
	return &calls
}

func TestScanFastPathSkipsRehashOnUnchangedLibrary(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "a.mp4", []byte("alpha content"))
	writeScanFile(t, root, "nested/b.mp4", []byte("beta content"))
	st := storetest.Open(t)
	prober := &countingProber{probe: ffmpeg.Probe{Container: "mp4", VideoCodec: "h264", HasVideo: true, DurationSeconds: 60}}
	hashes := countHashes(t)

	// First scan hashes and probes every file.
	first, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.FilesSeen != 2 {
		t.Fatalf("first scan FilesSeen = %d, want 2", first.FilesSeen)
	}
	if *hashes != 2 {
		t.Fatalf("first scan hash calls = %d, want 2", *hashes)
	}
	if prober.calls != 2 {
		t.Fatalf("first scan probe calls = %d, want 2", prober.calls)
	}

	// Second scan of the unchanged library: the (path, size, mtime) triple
	// matches, so it must perform ZERO hash and ZERO probe calls.
	*hashes = 0
	prober.calls = 0
	second, err := Scan(context.Background(), st, prober, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if second.FilesSeen != 2 {
		t.Fatalf("second scan FilesSeen = %d, want 2", second.FilesSeen)
	}
	if *hashes != 0 {
		t.Fatalf("second scan performed %d hash calls, want 0 (fast-path)", *hashes)
	}
	if prober.calls != 0 {
		t.Fatalf("second scan performed %d probe calls, want 0 (fast-path)", prober.calls)
	}
	// The fast-path still resolves the same content identity.
	if second.Files[0].ContentFingerprint != first.Files[0].ContentFingerprint &&
		second.Files[1].ContentFingerprint != first.Files[1].ContentFingerprint {
		t.Fatal("fast-path changed the content fingerprint")
	}
}

func TestScanFastPathRehashesWhenFileChanges(t *testing.T) {
	root := t.TempDir()
	path := writeScanFile(t, root, "a.mp4", []byte("original content"))
	st := storetest.Open(t)
	hashes := countHashes(t)

	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if *hashes != 1 {
		t.Fatalf("first scan hash calls = %d, want 1", *hashes)
	}

	// Rewrite the file with different-length content and a later mtime: a
	// changed (size, mtime) must force a re-hash so content identity stays
	// honest.
	if err := os.WriteFile(path, []byte("changed content is longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A changed but settled mtime (past the stability window) so the file is
	// still imported, just no longer fast-path-eligible.
	later := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}

	*hashes = 0
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if *hashes != 1 {
		t.Fatalf("changed file re-hash count = %d, want 1 (changed size/mtime must re-hash)", *hashes)
	}
}

func TestFullScanMarksDisappearedFilesMissing(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	gone := writeScanFile(t, root, "gone.mp4", []byte("gone content"))
	kept := writeScanFile(t, root, "kept.mp4", []byte("kept content"))
	st := storetest.Open(t)

	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("second scan: %v", err)
	}

	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != kept {
		t.Fatalf("live files = %#v, want only %q", files, kept)
	}
}

func TestYieldedScanDoesNotMarkDisappearedFilesMissing(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	gone := writeScanFile(t, root, "clip-000.mp4", []byte("gone content"))
	for i := 1; i < scanJobFileBatchSize+1; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	st := storetest.Open(t)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Add(10 * time.Millisecond)
	job, err := NewJob(JobRequest{
		ID:        "job-scan-yield-missing",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("RunOne returned error: %v", err)
	}
	loaded, err := st.Job(context.Background(), "job-scan-yield-missing")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.JobStatusYielded {
		t.Fatalf("job status = %q error=%q, want yielded", loaded.Status, loaded.Error)
	}
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != scanJobFileBatchSize+1 {
		t.Fatalf("yielded scan live files = %d, want %d", len(files), scanJobFileBatchSize+1)
	}
}

// TestScanMarksExtrasByConventionExcludedFromBrowse proves the ADR-0009 §3
// scan contract: a "-trailer"-suffixed file and a file under an Extras/ folder
// are imported as Extras (excluded from the default browse Source), while a
// peer feature file browses normally — and the Extras stay directly fetchable.
func TestScanMarksExtrasByConventionExcludedFromBrowse(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Feature.mp4", []byte("feature content"))
	writeScanFile(t, root, "Feature-trailer.mp4", []byte("trailer content"))
	writeScanFile(t, root, "Extras/Making Of.mp4", []byte("making-of content"))
	st := storetest.Open(t)

	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if result.FilesSeen != 3 {
		t.Fatalf("FilesSeen = %d, want 3", result.FilesSeen)
	}

	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}
	items, err := st.SourceItems(context.Background(), filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "Feature") {
		t.Fatalf("browse Source omitted the feature: %#v", items)
	}
	if hasSourceItemWithTitle(items, "Feature-trailer") {
		t.Fatalf("browse Source leaked the -trailer Extra: %#v", items)
	}
	if hasSourceItemWithTitle(items, "Making Of") {
		t.Fatalf("browse Source leaked the Extras/ folder file: %#v", items)
	}

	// The Extras are still directly addressable as Media items and grouped
	// under the sibling feature for detail surfaces.
	var featureID, trailerID, makingOfID store.MediaID
	for _, f := range result.Files {
		switch filepath.Base(f.Path) {
		case "Feature.mp4":
			featureID = f.MediaID
		case "Feature-trailer.mp4":
			trailerID = f.MediaID
		case "Making Of.mp4":
			makingOfID = f.MediaID
		}
	}
	if featureID == "" || trailerID == "" || makingOfID == "" {
		t.Fatalf("scan result missing feature/trailer/making-of ids: feature=%q trailer=%q makingOf=%q", featureID, trailerID, makingOfID)
	}
	if _, err := st.Media(context.Background(), trailerID, filter); err != nil {
		t.Fatalf("Extra not directly fetchable as Media: %v", err)
	}
	extras, err := st.MediaRelationsOf(context.Background(), featureID, store.MediaRelationExtraOf, filter)
	if err != nil {
		t.Fatalf("MediaRelationsOf(extra-of): %v", err)
	}
	gotExtras := map[store.MediaID]bool{}
	for _, extra := range extras {
		gotExtras[extra.ID] = true
	}
	if !gotExtras[trailerID] || !gotExtras[makingOfID] || len(gotExtras) != 2 {
		t.Fatalf("extras of feature = %#v, want trailer %q and making-of %q", extras, trailerID, makingOfID)
	}
}

// stubNow swaps the package-level clock seam for a fixed instant, restored at
// test cleanup, so the file-stability gate is deterministic.
func stubNow(t *testing.T, fixed time.Time) {
	t.Helper()
	original := now
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = original })
}

// TestScanSkipsFilesModifiedWithinStabilityWindow proves the ADR-0032 §4
// file-stability guard: a file whose mtime is inside the stability window
// (still streaming/copying) is skipped this pass and never fingerprinted,
// while a file with a settled (older) mtime is imported normally. The skipped
// file is not lost — a later scan picks it up once it settles.
func TestScanSkipsFilesModifiedWithinStabilityWindow(t *testing.T) {
	root := t.TempDir()
	fresh := writeScanFile(t, root, "streaming.mp4", []byte("still copying in"))
	settled := writeScanFile(t, root, "settled.mp4", []byte("done copying"))

	clock := time.Now()
	stubNow(t, clock)

	// One file modified just now (inside the window → unstable), one modified
	// comfortably before the window opened (stable).
	freshTime := clock.Add(-stabilityWindow / 2)
	if err := os.Chtimes(fresh, freshTime, freshTime); err != nil {
		t.Fatal(err)
	}
	settledTime := clock.Add(-2 * stabilityWindow)
	if err := os.Chtimes(settled, settledTime, settledTime); err != nil {
		t.Fatal(err)
	}

	st := storetest.Open(t)
	result, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}

	// Only the settled file is fingerprinted/imported; the still-streaming file
	// is skipped this pass.
	if result.FilesSeen != 1 {
		t.Fatalf("FilesSeen = %d, want 1 (fresh-mtime file must be skipped)", result.FilesSeen)
	}
	if len(result.Files) != 1 {
		t.Fatalf("imported %d files, want 1", len(result.Files))
	}
	if filepath.Base(result.Files[0].Path) != "settled.mp4" {
		t.Fatalf("imported %q, want settled.mp4 (the stable file)", result.Files[0].Path)
	}

	// A later scan, after the fresh file settles, picks it up — nothing lost.
	stubNow(t, clock.Add(2*stabilityWindow))
	second, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("second scan returned error: %v", err)
	}
	if second.FilesSeen != 2 {
		t.Fatalf("second scan FilesSeen = %d, want 2 (settled file now imported)", second.FilesSeen)
	}
}

// fakeMetadataHydrator records each HydrateMetadata call so scan-seam tests can
// assert the hook receives a generic Media shape plus probe metadata.
type fakeMetadataHydrator struct {
	capability MetadataHydrationCapability
	calls      []MetadataHydrationInput
	err        error
	onHydrate  func(MetadataHydrationInput)
}

func (f *fakeMetadataHydrator) Capability() MetadataHydrationCapability {
	return f.capability
}

func (f *fakeMetadataHydrator) HydrateMetadata(_ context.Context, input MetadataHydrationInput) error {
	f.calls = append(f.calls, input)
	if f.onHydrate != nil {
		f.onHydrate(input)
	}
	return f.err
}

// markingMetadataHydrator records its calls like fakeMetadataHydrator and
// writes the real hydration marker to the store on each call, so a backfill
// test can assert the marker is present afterward and that a second backfill (whose
// stale-set query now excludes the marked Media) is a no-op — the idempotency
// the absent-only marker buys. It writes only the marker, not the full graph,
// which is enough to drive the scan-side backfill seam.
type markingMetadataHydrator struct {
	st         *store.Store
	capability MetadataHydrationCapability
	calls      []store.MediaID
}

func (m *markingMetadataHydrator) Capability() MetadataHydrationCapability {
	return m.capability
}

func (m *markingMetadataHydrator) HydrateMetadata(ctx context.Context, input MetadataHydrationInput) error {
	m.calls = append(m.calls, input.MediaID)
	return m.st.InsertMediaFieldsIfAbsent(ctx, input.MediaID, []store.MediaField{
		{Name: m.capability.MarkerField, Type: store.EntityFieldFlag, Flag: true},
	})
}

// TestScanInvokesMetadataHydratorForAudioWithProbeMetadata asserts the existing
// audio fixture still reaches the adapter after ImportMediaFile, carrying its
// probe metadata and selected by the Library's plugin ID.
func TestScanInvokesMetadataHydratorForAudioWithProbeMetadata(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "Radiohead/OK Computer/02 Paranoid Android.flac", []byte("flac bytes here padding padding"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{"title": "Paranoid Android", "album": "OK Computer", "artist": "Radiohead", "track": "2/12"}
	prober := fakeProber{probe: ffmpeg.Probe{Container: "flac", AudioCodec: "flac", HasVideo: false, DurationSeconds: 387, Tags: tags}}
	hydrator := &fakeMetadataHydrator{capability: MetadataHydrationCapability{
		MediaClasses: []store.MediaClass{store.MediaClassAudio},
		MarkerField:  "music-hydrated",
	}}

	result, err := ScanWithMetadataHydration(context.Background(), st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"music": hydrator}, Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(hydrator.calls) != 1 {
		t.Fatalf("hydrator calls = %d, want 1", len(hydrator.calls))
	}
	call := hydrator.calls[0]
	if call.MediaID != result.Files[0].MediaID {
		t.Errorf("hydrator media = %q, want %q", call.MediaID, result.Files[0].MediaID)
	}
	if call.LibraryID != library.ID {
		t.Errorf("hydrator library = %q, want %q", call.LibraryID, library.ID)
	}
	if call.Class != store.MediaClassAudio {
		t.Errorf("hydrator class = %q, want %q", call.Class, store.MediaClassAudio)
	}
	if call.Metadata["album"] != "OK Computer" || call.Metadata["track"] != "2/12" {
		t.Errorf("hydrator did not receive probe metadata: %v", call.Metadata)
	}
}

func TestScanInvokesMetadataHydratorForNonMusicVideoCapability(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "archive/entry.mkv", []byte("video bytes here padding padding"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Archive", false, "archive", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{"catalog-code": "ARC-42"}
	prober := fakeProber{probe: ffmpeg.Probe{Container: "matroska", VideoCodec: "h264", HasVideo: true, Tags: metadata}}
	hydrator := &fakeMetadataHydrator{capability: MetadataHydrationCapability{
		MediaClasses: []store.MediaClass{store.MediaClassVideo},
		MarkerField:  "archive-hydrated",
	}}

	if _, err := ScanWithMetadataHydration(context.Background(), st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"archive": hydrator}, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(hydrator.calls) != 1 {
		t.Fatalf("hydrator calls = %d, want 1", len(hydrator.calls))
	}
	if call := hydrator.calls[0]; call.Class != store.MediaClassVideo || call.Metadata["catalog-code"] != "ARC-42" {
		t.Fatalf("video hydration input = %+v", call)
	}
}

// TestScanSkipsMetadataHydratorOutsideCapability asserts an image is not
// handed to a hook that declares only the audio Media shape.
func TestScanSkipsMetadataHydratorOutsideCapability(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "cover.jpg", []byte("\xff\xd8\xff\xe0 jpeg bytes"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(context.Background(), "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	hydrator := &fakeMetadataHydrator{capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassAudio}}}
	if _, err := ScanWithMetadataHydration(context.Background(), st, nil, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"music": hydrator}, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatalf("Scan returned error: %v", err)
	}
	if len(hydrator.calls) != 0 {
		t.Fatalf("hydrator called %d times for an image, want 0", len(hydrator.calls))
	}
}

func TestScanMetadataHydratorPreservesErrorAndCancellationBehavior(t *testing.T) {
	for _, tt := range []struct {
		name    string
		hookErr error
		wantErr error
	}{
		{name: "ordinary hook error skips the file", hookErr: errors.New("fixture hydration failed")},
		{name: "cancellation stops the scan", hookErr: context.Canceled, wantErr: context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			failingPath := writeScanFile(t, root, "01 failing.flac", []byte("audio bytes here padding padding"))
			var subsequentPath string
			if tt.wantErr == nil {
				subsequentPath = writeScanFile(t, root, "02 subsequent.jpg", []byte("\xff\xd8\xff\xe0 jpeg bytes"))
			}
			st := storetest.Open(t)
			library, err := st.CreateLibrary(context.Background(), "Library", false, "ledger", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			prober := fakeProber{probe: ffmpeg.Probe{Container: "flac", AudioCodec: "flac", Tags: map[string]string{"key": "value"}}}
			hydrator := &fakeMetadataHydrator{
				capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassAudio}},
				err:        tt.hookErr,
			}
			result, err := ScanWithMetadataHydration(context.Background(), st, prober, nil, nil, nil, nil, nil,
				map[string]MetadataHydrator{"ledger": hydrator}, Request{LibraryID: library.ID, Root: root})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Scan error = %v, want %v", err, tt.wantErr)
			}
			if len(hydrator.calls) != 1 {
				t.Fatalf("hydrator calls = %d, want 1", len(hydrator.calls))
			}
			if tt.wantErr == nil {
				completed := map[string]bool{}
				for _, file := range result.Files {
					completed[file.Path] = true
				}
				if completed[failingPath] {
					t.Fatalf("failing file %q was completed after hydration error", failingPath)
				}
				if !completed[subsequentPath] {
					t.Fatalf("subsequent file %q was not completed after ordinary hydration error; completed = %v", subsequentPath, completed)
				}
			}
		})
	}
}

func TestScanMetadataHydrationPrecedesCompletion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := writeScanFile(t, root, "entry.flac", []byte("audio bytes here padding padding"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Library", false, "ledger", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	hydrator := &fakeMetadataHydrator{
		capability: MetadataHydrationCapability{MediaClasses: []store.MediaClass{store.MediaClassAudio}},
		onHydrate: func(input MetadataHydrationInput) {
			events = append(events, "hydrate:"+input.Path)
		},
	}
	prober := fakeProber{probe: ffmpeg.Probe{Container: "flac", AudioCodec: "flac"}}
	_, err = scanFromCheckpoint(ctx, st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"ledger": hydrator}, nil, nil,
		Request{LibraryID: library.ID, Root: root}, jobCheckpoint{}, time.Now().UTC(),
		func(checkpoint jobCheckpoint, _ store.JobProgress) error {
			events = append(events, "complete:"+checkpoint.LastPath)
			return nil
		})
	if err != nil {
		t.Fatalf("scanFromCheckpoint returned error: %v", err)
	}
	want := []string{"hydrate:" + path, "complete:" + path}
	if got := strings.Join(events, "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("scan events = %q, want hydration before completion %q", events, want)
	}
}

// TestScanBackfillHydratesAudioLackingMarker exercises the MUSIC-1 backfill
// branch: an audio file imported without the hydration marker is re-probed and
// hydrated on a backfill scan via the fast path, while an already-marked file is
// left alone, and a second backfill is a no-op (the absent-only marker makes it
// idempotent). A normal (non-backfill) scan never re-probes or re-hydrates.
func TestScanBackfillHydratesAudioLackingMarker(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeScanFile(t, root, "Artist/Album/01 stale.flac", []byte("flac stale bytes padding"))
	writeScanFile(t, root, "Artist/Album/02 marked.flac", []byte("flac marked bytes padding"))
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Music", false, "music", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	tags := map[string]string{"title": "Stale", "album": "Album", "artist": "Artist"}
	prober := fakeProber{probe: ffmpeg.Probe{Container: "flac", AudioCodec: "flac", HasVideo: false, DurationSeconds: 200, Tags: tags}}

	// First scan with NO hydrator: both files import as audio (the probe sets the
	// class) but neither gets the hydration marker — the fast-path-imported /
	// pre-hydration state the backfill must repair.
	first, err := ScanWithMetadataHydration(ctx, st, prober, nil, nil, nil, nil, nil, nil,
		Request{LibraryID: library.ID, Root: root})
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.FilesSeen != 2 {
		t.Fatalf("first scan FilesSeen = %d, want 2", first.FilesSeen)
	}
	byBase := map[string]store.MediaID{}
	for _, f := range first.Files {
		byBase[filepath.Base(f.Path)] = f.MediaID
	}
	stale := byBase["01 stale.flac"]
	marked := byBase["02 marked.flac"]
	if stale == "" || marked == "" {
		t.Fatalf("missing imported media IDs: %v", byBase)
	}
	// Mark one file as already hydrated, so the backfill stale set is exactly the
	// other one.
	if err := st.InsertMediaFieldsIfAbsent(ctx, marked, []store.MediaField{
		{Name: "music-hydrated", Type: store.EntityFieldFlag, Flag: true},
	}); err != nil {
		t.Fatal(err)
	}

	// A NORMAL (non-backfill) scan must not re-probe or hydrate anything, even
	// with a hydrator wired — the marker-less file stays marker-less.
	capability := MetadataHydrationCapability{
		MediaClasses: []store.MediaClass{store.MediaClassAudio},
		MarkerField:  "music-hydrated",
	}
	normalHydrator := &markingMetadataHydrator{st: st, capability: capability}
	if _, err := ScanWithMetadataHydration(ctx, st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"music": normalHydrator}, Request{LibraryID: library.ID, Root: root}); err != nil {
		t.Fatalf("normal scan: %v", err)
	}
	if len(normalHydrator.calls) != 0 {
		t.Fatalf("non-backfill scan hydrated %d files, want 0", len(normalHydrator.calls))
	}
	if got, err := st.MediaLackingFieldInLibrary(ctx, library.ID, store.MediaClassAudio, "music-hydrated"); err != nil {
		t.Fatal(err)
	} else if _, ok := got[stale]; !ok {
		t.Fatal("stale file unexpectedly hydrated by a non-backfill scan")
	}

	// BACKFILL scan: hydrate only the marker-less audio file.
	hydrator := &markingMetadataHydrator{st: st, capability: capability}
	if _, err := ScanWithMetadataHydration(ctx, st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"music": hydrator}, Request{LibraryID: library.ID, Root: root, Backfill: true}); err != nil {
		t.Fatalf("backfill scan: %v", err)
	}
	if len(hydrator.calls) != 1 {
		t.Fatalf("backfill hydrated %d files, want 1", len(hydrator.calls))
	}
	if hydrator.calls[0] != stale {
		t.Errorf("backfill hydrated %q, want the stale file %q", hydrator.calls[0], stale)
	}
	// The marker is written after backfill — the stale set is now empty.
	remaining, err := st.MediaLackingFieldInLibrary(ctx, library.ID, store.MediaClassAudio, "music-hydrated")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("after backfill, %d audio files still lack the marker, want 0: %v", len(remaining), remaining)
	}

	// Idempotency: a second backfill finds nothing stale and hydrates nothing.
	again := &markingMetadataHydrator{st: st, capability: capability}
	if _, err := ScanWithMetadataHydration(ctx, st, prober, nil, nil, nil, nil, nil,
		map[string]MetadataHydrator{"music": again}, Request{LibraryID: library.ID, Root: root, Backfill: true}); err != nil {
		t.Fatalf("second backfill scan: %v", err)
	}
	if len(again.calls) != 0 {
		t.Fatalf("second backfill hydrated %d files, want 0 (idempotent)", len(again.calls))
	}
}

// TestJobPayloadVersionRoundTrip asserts the v1→v2 payload migration: a v1
// payload (no backfill field) loads with Backfill=false, and a v2 backfill
// payload round-trips Backfill=true.
func TestJobPayloadVersionRoundTrip(t *testing.T) {
	// A legacy v1 payload omits "backfill" entirely.
	v1 := []byte(`{"version":1,"library_id":"lib-1","root":"/media"}`)
	req, err := requestFromJobPayload(v1)
	if err != nil {
		t.Fatalf("v1 payload load: %v", err)
	}
	if req.Backfill {
		t.Errorf("v1 payload Backfill = true, want false")
	}
	if req.LibraryID != "lib-1" || req.Root != "/media" {
		t.Errorf("v1 payload decoded %+v", req)
	}

	// A v2 backfill job round-trips through NewJob → requestFromJobPayload.
	job, err := NewJob(JobRequest{LibraryID: "lib-1", Root: "/media", Backfill: true})
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	back, err := requestFromJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("v2 payload load: %v", err)
	}
	if !back.Backfill {
		t.Errorf("v2 payload Backfill = false, want true")
	}
	if back.LibraryID != "lib-1" {
		t.Errorf("v2 payload LibraryID = %q, want lib-1", back.LibraryID)
	}
}
