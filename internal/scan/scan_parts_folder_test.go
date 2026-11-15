package scan

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// taggedPartsParser follows the external parts plugin's contract: title from
// the album tag (else the folder), part index from the track tag (else a
// leading filename ordinal), and in a multi-file folder it accepts a file only
// when both tags are present.
type taggedPartsParser struct{}

func (taggedPartsParser) Parse(path string, probe ParseProbe) (ParsedMedia, bool) {
	title := probe.Tags["album"]
	if title == "" {
		title = filepath.Base(filepath.Dir(path))
	}
	index, _ := strconv.Atoi(strings.Split(probe.Tags["track"], "/")[0])
	if index <= 0 {
		_, _ = fmt.Sscanf(filepath.Base(path), "%d", &index)
	}
	parsed := ParsedMedia{Title: title, Class: "collection"}
	if index > 0 {
		parsed.Index, parsed.PartGroup = index, filepath.Clean(filepath.Dir(path))
	}
	return parsed, true
}

func (p taggedPartsParser) ParseWithContext(path string, probe ParseProbe, context ParseContext) (ParsedMedia, bool) {
	parsed, ok := p.Parse(path, probe)
	if context.DirectoryMediaFileCount == DirectoryMediaFileCountMultiple {
		track, _ := strconv.Atoi(strings.Split(probe.Tags["track"], "/")[0])
		if probe.Tags["album"] == "" || track <= 0 {
			return ParsedMedia{}, false
		}
	}
	return parsed, ok
}

// taggedPartsProber returns audio probes whose tags are keyed by file name.
type taggedPartsProber map[string]map[string]string

func (p taggedPartsProber) Probe(_ context.Context, path string) (ffmpeg.Probe, error) {
	return ffmpeg.Probe{AudioCodec: "mp3", DurationSeconds: 60, Tags: p[filepath.Base(path)]}, nil
}

func writePartsFolder(t *testing.T, root string) (dir string, parts []string) {
	t.Helper()
	dir = filepath.Join(root, "Writer", "Tale")
	for _, name := range []string{"02 - Part Two.mp3", "01 - Part One.mp3", "03 - Part Three.mp3"} {
		parts = append(parts, writeScanFile(t, dir, name, []byte(name)))
	}
	parts[0], parts[1] = parts[1], parts[0]
	writeScanFile(t, dir, "cover.jpg", jpegBytes("cover"))
	return dir, parts
}

func partsLibrary(t *testing.T, st *store.Store) store.LibraryID {
	t.Helper()
	library, err := st.CreateLibrary(context.Background(), "Parts", false, "parts", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	return library.ID
}

// TestScanCataloguesTaggedPartsFolderBesideImages (CORE-1710, CORE-1833):
// three tagged audio parts beside cover.jpg and an out-of-vocabulary image are
// one item whose files play in part order. Neither image may refuse the folder,
// and neither is catalogued, on the first scan or a rescan.
func TestScanCataloguesTaggedPartsFolderBesideImages(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		dir, parts := writePartsFolder(t, root)
		stray := writeScanFile(t, dir, "back.jpg", jpegBytes("back"))
		prober := taggedPartsProber{}
		for i, part := range parts {
			prober[filepath.Base(part)] = map[string]string{"album": "Synthetic Tale", "track": fmt.Sprintf("%d/3", i+1)}
		}
		st := storetest.Open(t)
		libraryID := partsLibrary(t, st)
		filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}}
		parsers := map[string]FilenameParser{"parts": taggedPartsParser{}}

		for pass := 1; pass <= 2; pass++ {
			if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root}); err != nil {
				t.Fatalf("workers=%d pass=%d scan: %v", workers, pass, err)
			}
		}
		records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: filter.AllowedIDs, Revealed: true})
		if err != nil || len(records) != 0 {
			t.Fatalf("workers=%d unmatched = %#v, %v; want none", workers, records, err)
		}
		files, err := st.MediaFilesForLibrary(context.Background(), libraryID)
		if err != nil {
			t.Fatal(err)
		}
		var mediaID store.MediaID
		for _, file := range files {
			if file.Path == parts[0] {
				mediaID = file.MediaID
			}
		}
		if mediaID == "" {
			t.Fatalf("workers=%d first part not catalogued; files = %#v", workers, files)
		}
		media, err := st.Media(context.Background(), mediaID, filter)
		if err != nil || media.Title != "Synthetic Tale" {
			t.Fatalf("workers=%d media = %#v, %v; want title Synthetic Tale", workers, media, err)
		}
		grouped, err := st.MediaFilesForMedia(context.Background(), mediaID, filter)
		if err != nil || len(grouped) != len(parts) {
			t.Fatalf("workers=%d grouped files = %#v, %v; want %d parts", workers, grouped, err, len(parts))
		}
		for i, file := range grouped {
			if file.Path != parts[i] || file.PartOrdinal != i {
				t.Fatalf("workers=%d part %d = {path:%q ordinal:%d}, want {path:%q ordinal:%d}", workers, i, file.Path, file.PartOrdinal, parts[i], i)
			}
		}
		for _, file := range files {
			if file.Path == stray || filepath.Base(file.Path) == "cover.jpg" {
				t.Fatalf("workers=%d image catalogued: %#v", workers, file)
			}
		}
		if len(files) != len(parts) {
			t.Fatalf("workers=%d files = %#v; want only the %d parts", workers, files, len(parts))
		}
	}
}

// TestScanDropsCataloguedPartsFolderImagesPastRatioGuard (CORE-1833): images
// catalogued before their folder was recognised as parts leave the catalog on
// the rescan that skips them, even when there are enough of them to trip the
// missing-ratio guard that holds back the end-of-scan missing pass.
func TestScanDropsCataloguedPartsFolderImagesPastRatioGuard(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	dir, parts := writePartsFolder(t, root)
	for i := 1; i <= missingRatioGuardMinPresent; i++ {
		writeScanFile(t, dir, fmt.Sprintf("page-%02d.jpg", i), jpegBytes(fmt.Sprintf("page %d", i)))
	}
	prober := taggedPartsProber{}
	for i, part := range parts {
		prober[filepath.Base(part)] = map[string]string{"album": "Synthetic Tale", "track": fmt.Sprintf("%d/3", i+1)}
	}
	st := storetest.Open(t)
	libraryID := partsLibrary(t, st)
	if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, nil, nil, nil, nil, nil, fixedGovernor(1), Request{LibraryID: libraryID, Root: root}); err != nil {
		t.Fatalf("scan without the parts parser: %v", err)
	}
	before, err := st.MediaFilesForLibrary(context.Background(), libraryID)
	if err != nil {
		t.Fatal(err)
	}
	images := 0
	for _, file := range before {
		if imageClassForPath(file.Path) {
			images++
		}
	}
	if images < missingRatioGuardMinPresent {
		t.Fatalf("first scan catalogued %d images, want at least %d; files = %#v", images, missingRatioGuardMinPresent, before)
	}
	parsers := map[string]FilenameParser{"parts": taggedPartsParser{}}
	if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(1), Request{LibraryID: libraryID, Root: root}); err != nil {
		t.Fatalf("rescan with the parts parser: %v", err)
	}
	after, err := st.MediaFilesForLibrary(context.Background(), libraryID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range after {
		if imageClassForPath(file.Path) {
			t.Fatalf("image still live after the rescan: %q", file.Path)
		}
	}
}

// TestScanLogsRefusedPartsFolder (CORE-1710): a folder the plugin refuses is
// queued as unmatched and the scan log names the folder, the refused file and
// the reason, so it is never silently missing. A folder of images alone keeps
// the folder-level refusal.
func TestScanLogsRefusedPartsFolder(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	dir, parts := writePartsFolder(t, root)
	images := filepath.Join(root, "Writer", "Stills")
	writeScanFile(t, images, "one.jpg", jpegBytes("one"))
	writeScanFile(t, images, "two.jpg", jpegBytes("two"))
	st := storetest.Open(t)
	libraryID := partsLibrary(t, st)
	logs := captureScanLogs(t)

	// No tags: the plugin refuses every part in the multi-file folder.
	if _, err := Scan(context.Background(), st, taggedPartsProber{}, nil, map[string]FilenameParser{"parts": taggedPartsParser{}}, nil, nil, Request{LibraryID: libraryID, Root: root}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	files, err := st.MediaFilesForLibrary(context.Background(), libraryID)
	if err != nil || len(files) != 0 {
		t.Fatalf("files = %#v, %v; want none", files, err)
	}
	records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}, Revealed: true})
	if err != nil {
		t.Fatal(err)
	}
	queued := map[string]string{}
	for _, record := range records {
		queued[record.SourceHandle] = record.FailureReason
	}
	if len(records) != 2 || queued[dir] != store.UnmatchedReasonAmbiguous || queued[images] != store.UnmatchedReasonAmbiguous {
		t.Fatalf("unmatched = %#v, want both folders queued as ambiguous", records)
	}
	out := logs.String()
	if got := strings.Count(out, "folder queued as unmatched"); got != 2 {
		t.Fatalf("refusal logged %d times, want once per folder:\n%s", got, out)
	}
	for _, want := range []string{"folder=" + dir, "file=\"" + parts[0] + "\"", "plugin=parts", "reason=ambiguous"} {
		if !strings.Contains(out, want) {
			t.Fatalf("scan log lacks %q:\n%s", want, out)
		}
	}
}

// TestScanKeepsPartsGroupAcrossInterleavedImage (CORE-1837): an image whose
// name sorts between numbered parts must not split the parts into separate
// items, and a rescan must keep every part present.
func TestScanKeepsPartsGroupAcrossInterleavedImage(t *testing.T) {
	// feature:scan-pipeline
	for _, workers := range []int{1, 4} {
		root := t.TempDir()
		dir := filepath.Join(root, "Writer", "Tale")
		parts := []string{
			writeScanFile(t, dir, "01.mp3", []byte("part one")),
			writeScanFile(t, dir, "03.mp3", []byte("part two")),
		}
		writeScanFile(t, dir, "02.jpg", jpegBytes("between"))
		prober := taggedPartsProber{
			"01.mp3": {"album": "Synthetic Tale", "track": "1/2"},
			"03.mp3": {"album": "Synthetic Tale", "track": "2/2"},
		}
		st := storetest.Open(t)
		libraryID := partsLibrary(t, st)
		filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{libraryID}}
		parsers := map[string]FilenameParser{"parts": taggedPartsParser{}}

		for pass := 1; pass <= 2; pass++ {
			if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root}); err != nil {
				t.Fatalf("workers=%d pass=%d scan: %v", workers, pass, err)
			}
			files, err := st.MediaFilesForLibrary(context.Background(), libraryID)
			if err != nil {
				t.Fatal(err)
			}
			partMedia := map[store.MediaID]bool{}
			for _, file := range files {
				if filepath.Ext(file.Path) != ".mp3" {
					continue
				}
				partMedia[file.MediaID] = true
				if !file.MissingAt.IsZero() {
					t.Fatalf("workers=%d pass=%d part %q marked missing", workers, pass, file.Path)
				}
			}
			if len(partMedia) != 1 {
				t.Fatalf("workers=%d pass=%d parts split across %d items: %#v", workers, pass, len(partMedia), files)
			}
			var mediaID store.MediaID
			for id := range partMedia {
				mediaID = id
			}
			grouped, err := st.MediaFilesForMedia(context.Background(), mediaID, filter)
			if err != nil || len(grouped) != len(parts) {
				t.Fatalf("workers=%d pass=%d grouped files = %#v, %v; want %d parts", workers, pass, grouped, err, len(parts))
			}
			for i, file := range grouped {
				if file.Path != parts[i] || file.PartOrdinal != i || !file.MissingAt.IsZero() {
					t.Fatalf("workers=%d pass=%d part %d = {path:%q ordinal:%d missing:%v}, want {path:%q ordinal:%d present}", workers, pass, i, file.Path, file.PartOrdinal, file.MissingAt, parts[i], i)
				}
			}
		}
	}
}

// TestCompletionKeepsCheckpointBelowPendingParts (CORE-1837): an image that
// completes while parts are pending leaves the resume checkpoint below the
// first pending part, so a resumed scan re-walks the whole group.
func TestCompletionKeepsCheckpointBelowPendingParts(t *testing.T) {
	// feature:scan-pipeline
	root := t.TempDir()
	dir := filepath.Join(root, "Writer", "Tale")
	before := &completedFile{path: filepath.Join(root, "Writer", "Earlier", "one.jpg"), mediaID: "media-a", done: make(chan struct{})}
	between := &completedFile{path: filepath.Join(dir, "02.jpg"), mediaID: "media-b", done: make(chan struct{})}
	close(before.done)
	close(between.done)
	s := &scanWalkState{
		ctx:                context.Background(),
		req:                Request{Root: root},
		result:             Result{seenMedia: map[store.MediaID]bool{}},
		pendingParts:       []parsedPartImport{{imported: store.ImportMediaFile{Path: filepath.Join(dir, "01.mp3")}}},
		pendingCompletions: []*completedFile{before, between},
	}
	reports := 0
	s.progress = func(jobCheckpoint, store.JobProgress) error {
		reports++
		return nil
	}
	if err := s.completeFirst(); err != nil || s.result.LastPath != before.path {
		t.Fatalf("after earlier file: LastPath = %q, %v; want %q", s.result.LastPath, err, before.path)
	}
	if err := s.completeFirst(); err != nil || s.result.LastPath != before.path {
		t.Fatalf("after interleaved image: LastPath = %q, %v; want it held at %q", s.result.LastPath, err, before.path)
	}
	if s.result.FilesSeen != 2 {
		t.Fatalf("FilesSeen = %d, want 2", s.result.FilesSeen)
	}
	// Each report counts toward the job's yield batch; a held one would
	// let a batch of images yield onto the same checkpoint forever.
	if reports != 1 {
		t.Fatalf("progress reports = %d, want 1 (none for the held image)", reports)
	}
}
