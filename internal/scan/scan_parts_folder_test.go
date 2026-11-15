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

// TestScanCataloguesTaggedPartsFolderBesideImages (CORE-1710): three tagged
// audio parts beside cover.jpg and a stray image are one item whose files play
// in part order. Neither image may refuse the folder.
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

		if _, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: libraryID, Root: root}); err != nil {
			t.Fatalf("workers=%d scan: %v", workers, err)
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
			if file.Path == stray && file.MediaID == mediaID {
				t.Fatalf("workers=%d stray image joined the parts item", workers)
			}
			if filepath.Base(file.Path) == "cover.jpg" {
				t.Fatalf("workers=%d cover.jpg catalogued: %#v", workers, file)
			}
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
