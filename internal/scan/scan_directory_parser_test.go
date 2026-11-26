package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type stubContextReasonParser struct {
	refuseFile string
	reason     string
}

func (p stubContextReasonParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	base := filepath.Base(path)
	dir := filepath.Clean(filepath.Dir(path))
	return ParsedMedia{
		Title:     filepath.Base(dir),
		Class:     "audio",
		Index:     1,
		PartGroup: dir,
	}, base != ""
}

func (p stubContextReasonParser) ParseWithContext(path string, probe ParseProbe, ctx ParseContext) (ParsedMedia, bool) {
	parsed, ok := p.Parse(path, probe)
	if !ok {
		return ParsedMedia{}, false
	}
	if ctx.DirectoryMediaFileCount == DirectoryMediaFileCountMultiple && filepath.Base(path) == p.refuseFile {
		return ParsedMedia{}, false
	}
	return parsed, true
}

func (p stubContextReasonParser) ContextRefusalReason(path string, _ ParseProbe, ctx ParseContext) string {
	if ctx.DirectoryMediaFileCount == DirectoryMediaFileCountMultiple && filepath.Base(path) == p.refuseFile {
		return p.reason
	}
	return ""
}

type stubDirectoryParser struct {
	calls          int
	lastDir        string
	lastProbeCount int
	lastSubdirs    bool
	refuseReason   string
}

func (p *stubDirectoryParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	dir := filepath.Clean(filepath.Dir(path))
	return ParsedMedia{
		Title: filepath.Base(dir),
		Class: "audio",
	}, true
}

func (p *stubDirectoryParser) ParseDirectory(dir string, probes map[string]ParseProbe, hasMediaSubdirs bool) (map[string]ParsedMedia, string, bool) {
	p.calls++
	p.lastDir = dir
	p.lastProbeCount = len(probes)
	p.lastSubdirs = hasMediaSubdirs
	if hasMediaSubdirs {
		return nil, "", false
	}
	if p.refuseReason != "" {
		return nil, p.refuseReason, true
	}
	paths := make([]string, 0, len(probes))
	for path := range probes {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	title := strings.TrimSuffix(filepath.Base(dir), " (Unabridged)")
	out := make(map[string]ParsedMedia, len(paths))
	for i, path := range paths {
		out[path] = ParsedMedia{
			Title:     title,
			Class:     "audio",
			Index:     i + 1,
			PartGroup: filepath.Clean(dir),
		}
	}
	return out, "", true
}

func (p *stubDirectoryParser) PartMarkerTitle(path string, probe ParseProbe) string {
	if t := strings.TrimSpace(probe.Tags["title"]); t != "" {
		return t
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.ReplaceAll(stem, "_", " ")
}

func captureJSONLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestScanRefusalReasonLoggedAndStoredCORE2025(t *testing.T) {
	tests := []struct {
		name       string
		reason     string
		wantReason string
	}{
		{
			name:       "plugin supplied refusal reason",
			reason:     "mixed collection items in folder",
			wantReason: "mixed collection items in folder",
		},
		{
			name:       "empty reason falls back to ambiguous",
			reason:     "",
			wantReason: store.UnmatchedReasonAmbiguous,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureJSONLogs(t)
			root := t.TempDir()
			writeScanFile(t, root, "Mixed/01.mp3", []byte("one"))
			writeScanFile(t, root, "Mixed/extra.mp3", []byte("two"))

			st := storetest.Open(t)
			lib, err := st.CreateLibrary(context.Background(), "Audio", false, "stub-plugin", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			parsers := map[string]FilenameParser{
				"stub-plugin": stubContextReasonParser{refuseFile: "extra.mp3", reason: tt.reason},
			}
			if _, err := Scan(context.Background(), st, nil, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
				t.Fatalf("Scan returned error: %v", err)
			}

			records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
			if err != nil {
				t.Fatal(err)
			}
			if len(records) != 1 || records[0].FailureReason != tt.wantReason {
				t.Fatalf("UnmatchedRecords = %+v, want 1 record with FailureReason=%q", records, tt.wantReason)
			}

			foundReasonLog := false
			for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
				if line == "" {
					continue
				}
				var entry map[string]any
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					continue
				}
				if entry["msg"] == "scan: library plugin refused a file in a multi-file folder; folder queued as unmatched" {
					if entry["reason"] == tt.wantReason {
						foundReasonLog = true
					}
				}
			}
			if !foundReasonLog {
				t.Fatalf("expected scan refusal log with reason=%q; logs:\n%s", tt.wantReason, logs.String())
			}
		})
	}
}

func TestScanDirectoryParserAndImageSubdirHandling(t *testing.T) {
	t.Run("groups flat multi-part folder with image and image-only subdir", func(t *testing.T) {
		root := t.TempDir()
		p1 := writeScanFile(t, root, "Collection (Unabridged)/Part_01.mp3", []byte("part one"))
		p2 := writeScanFile(t, root, "Collection (Unabridged)/Part_02.mp3", []byte("part two"))
		writeScanFile(t, root, "Collection (Unabridged)/cover.jpg", []byte("jpg"))
		writeScanFile(t, root, "Collection (Unabridged)/Artwork/booklet.jpg", []byte("jpg2"))

		st := storetest.Open(t)
		lib, err := st.CreateLibrary(context.Background(), "Audio", false, "dir-plugin", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		prober := &pathProber{probes: map[string]ffmpeg.Probe{
			p1: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 100, Tags: map[string]string{"title": "Opening Tag"}},
			p2: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 150},
		}}
		dp := &stubDirectoryParser{}
		if _, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{"dir-plugin": dp}, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
			t.Fatalf("Scan returned error: %v", err)
		}
		if dp.lastProbeCount != 2 || dp.lastSubdirs {
			t.Fatalf("ParseDirectory saw probeCount=%d subdirs=%v, want 2 non-image probes, subdirs=false", dp.lastProbeCount, dp.lastSubdirs)
		}

		items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}, store.SourceQuery{Kind: "recently-added"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AnnotateMediaClass(context.Background(), items); err != nil {
			t.Fatal(err)
		}
		var grouped *store.SourceItem
		for i := range items {
			if items[i].Title == "cover" {
				t.Fatalf("cover.jpg inside multi-part folder should have been skipped: %+v", items)
			}
			if items[i].Title == "Collection" {
				grouped = &items[i]
			}
		}
		if grouped == nil {
			t.Fatalf("missing grouped item \"Collection\" in %+v", items)
		}
		if grouped.Summary != "5 min" || grouped.DurationSeconds != 250 {
			t.Fatalf("SourceItem = %+v, want Summary=\"5 min\" DurationSeconds=250 (sum 100+150)", *grouped)
		}
		media, err := st.Media(context.Background(), grouped.ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if media.Title != "Collection" || media.DurationSeconds != 250 {
			t.Fatalf("Media = %+v, want Title=\"Collection\" DurationSeconds=250", media)
		}
		fields, err := st.MediaFields(context.Background(), media.ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
		if err != nil {
			t.Fatal(err)
		}
		mf := mediaFieldMap(fields)
		if _, hasIndex := mf["index"]; hasIndex {
			t.Fatalf("grouped Media unexpectedly wrote index field: %+v", mf["index"])
		}
		if _, hasIndexEnd := mf["index-end"]; hasIndexEnd {
			t.Fatalf("grouped Media unexpectedly wrote index-end field: %+v", mf["index-end"])
		}

		markers, err := st.MarkersForMedia(context.Background(), media.ID, store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if len(markers) != 2 || markers[0].Title != "Opening Tag" || markers[1].Title != "Part 02" {
			t.Fatalf("markers = %+v, want [Opening Tag, Part 02]", markers)
		}
	})

	t.Run("directory refusal records reason cleanly", func(t *testing.T) {
		root := t.TempDir()
		p1 := writeScanFile(t, root, "Mixed/a.mp3", []byte("a"))
		p2 := writeScanFile(t, root, "Mixed/b.mp3", []byte("b"))
		st := storetest.Open(t)
		lib, err := st.CreateLibrary(context.Background(), "Audio", false, "dir-plugin", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		prober := &pathProber{probes: map[string]ffmpeg.Probe{
			p1: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 60},
			p2: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 60},
		}}
		dp := &stubDirectoryParser{refuseReason: "folder mixes incompatible albums"}
		if _, err := Scan(context.Background(), st, prober, nil, map[string]FilenameParser{"dir-plugin": dp}, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
			t.Fatalf("Scan error: %v", err)
		}
		records, err := st.UnmatchedRecords(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}})
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || records[0].FailureReason != "folder mixes incompatible albums" {
			t.Fatalf("records = %+v, want custom directory refusal reason", records)
		}
	})

	t.Run("directory parser delegates when media subdirectories coexist with loose files", func(t *testing.T) {
		root := t.TempDir()
		writeScanFile(t, root, "Multi/Disc 1/01.mp3", []byte("d1"))
		writeScanFile(t, root, "Multi/LooseA.mp3", []byte("a"))
		writeScanFile(t, root, "Multi/LooseB.mp3", []byte("b"))
		st := storetest.Open(t)
		lib, err := st.CreateLibrary(context.Background(), "Audio", false, "dir-plugin", "profile-default")
		if err != nil {
			t.Fatal(err)
		}
		dp := &stubDirectoryParser{}
		if _, err := Scan(context.Background(), st, nil, nil, map[string]FilenameParser{"dir-plugin": dp}, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
			t.Fatalf("Scan error: %v", err)
		}
		if dp.calls != 0 || dp.lastSubdirs {
			t.Fatalf("expected ParseDirectory skipped when hasMediaSubdirs=true; got calls=%d", dp.calls)
		}
	})
}

func TestScanMultiPartRescanHealsStaleDurationAndClearsIndexFields(t *testing.T) {
	root := t.TempDir()
	p1 := writeScanFile(t, root, "Author/Novel/01 - Opening.mp3", []byte("part1"))
	p2 := writeScanFile(t, root, "Author/Novel/02 - Ending.mp3", []byte("part2"))

	st := storetest.Open(t)
	lib, err := st.CreateLibrary(context.Background(), "Audio", false, "ord", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}
	prober := &pathProber{probes: map[string]ffmpeg.Probe{
		p1: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 120},
		p2: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 180},
	}}
	parsers := map[string]FilenameParser{"ord": ordinalParser{}}

	res, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root})
	if err != nil {
		t.Fatalf("initial Scan error: %v", err)
	}
	mediaID := res.Files[0].MediaID
	media, err := st.Media(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if media.DurationSeconds != 300 {
		t.Fatalf("initial Media.DurationSeconds = %d, want 300", media.DurationSeconds)
	}

	// Simulate pre-fix catalog state where the parser wrote Part 1 duration and stale index fields.
	if err := st.RefreshParserMediaFields(context.Background(), mediaID, store.ParserFieldRefresh{
		Authoritative: true,
		Fields: []store.MediaField{
			{Name: "class", Type: store.EntityFieldText, Text: "audio"},
			{Name: "index", Type: store.EntityFieldNumber, Number: 1},
			{Name: "index-end", Type: store.EntityFieldNumber, Number: 2},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetMultiPartMediaDuration(context.Background(), mediaID, 120); err != nil {
		t.Fatal(err)
	}
	stale, err := st.Media(context.Background(), mediaID, filter)
	if err != nil || stale.DurationSeconds != 120 {
		t.Fatalf("expected simulated stale duration 120, got %+v err=%v", stale, err)
	}

	// Ensure mtimes are unchanged so the second scan takes the cheap-triple fast path.
	if info, err := os.Stat(p1); err != nil || info.Size() == 0 {
		t.Fatal(err)
	}
	prober.calls = nil
	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
		t.Fatalf("rescan error: %v", err)
	}
	healed, err := st.Media(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if healed.DurationSeconds != 300 {
		t.Fatalf("rescan healed DurationSeconds = %d, want 300", healed.DurationSeconds)
	}
	fields, err := st.MediaFields(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	mf := mediaFieldMap(fields)
	if _, hasIndex := mf["index"]; hasIndex {
		t.Fatalf("rescan left stale index field: %+v", mf["index"])
	}
	if _, hasIndexEnd := mf["index-end"]; hasIndexEnd {
		t.Fatalf("rescan left stale index-end field: %+v", mf["index-end"])
	}
	items, err := st.SourceItems(context.Background(), filter, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AnnotateMediaClass(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Summary != "5 min" || items[0].DurationSeconds != 300 {
		t.Fatalf("SourceItems after heal = %+v, want Summary=\"5 min\" DurationSeconds=300", items)
	}
}

type discSingleFileParser struct{}

func (discSingleFileParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	discDir := filepath.Base(filepath.Dir(path))
	bookDir := filepath.Clean(filepath.Dir(filepath.Dir(path)))
	idx := 10001
	if strings.Contains(discDir, "2") {
		idx = 20001
	}
	return ParsedMedia{
		Title:     filepath.Base(bookDir),
		Class:     "audio",
		Index:     idx,
		PartGroup: bookDir,
	}, true
}

func (discSingleFileParser) PartMarkerTitle(path string, probe ParseProbe) string {
	if t := strings.TrimSpace(probe.Tags["title"]); t != "" {
		return t
	}
	return filepath.Base(filepath.Dir(path)) + " · Part 1"
}

func TestScanDiscFastPathHealsDurationFromStoredMarkersWithoutClobberingTitles(t *testing.T) {
	root := t.TempDir()
	p1 := writeScanFile(t, root, "Novel/Disc 1/01.mp3", []byte("disc1"))
	p2 := writeScanFile(t, root, "Novel/Disc 2/01.mp3", []byte("disc2"))

	st := storetest.Open(t)
	lib, err := st.CreateLibrary(context.Background(), "Audio", false, "disc-plugin", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}
	prober := &pathProber{probes: map[string]ffmpeg.Probe{
		p1: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 120, Tags: map[string]string{"title": "Disc 1 Custom"}},
		p2: {Container: "mp3", AudioCodec: "mp3", DurationSeconds: 180, Tags: map[string]string{"title": "Disc 2 Custom"}},
	}}
	parsers := map[string]FilenameParser{"disc-plugin": discSingleFileParser{}}

	res, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	mediaID := res.Files[0].MediaID
	if _, err := st.SetMultiPartMediaDuration(context.Background(), mediaID, 120); err != nil {
		t.Fatal(err)
	}
	prober.calls = nil
	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
		t.Fatal(err)
	}
	healed, err := st.Media(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if healed.DurationSeconds != 300 {
		t.Fatalf("healed DurationSeconds = %d, want 300 from stored markers", healed.DurationSeconds)
	}
	if len(prober.calls) != 0 {
		t.Fatalf("unexpected prober calls on unchanged fast-path disc rescan: %v", prober.calls)
	}
	markers, err := st.MarkersForMedia(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 2 || markers[0].Title != "Disc 1 Custom" || markers[1].Title != "Disc 2 Custom" {
		t.Fatalf("fast-path rescan clobbered custom marker titles: %+v", markers)
	}
}

func TestScanDiscFastPathHealsEmbeddedChaptersAndClearsProbeOwnedIndices(t *testing.T) {
	root := t.TempDir()
	p1 := writeScanFile(t, root, "Novel/Disc 1/01.m4b", []byte("disc1"))
	p2 := writeScanFile(t, root, "Novel/Disc 2/01.m4b", []byte("disc2"))

	st := storetest.Open(t)
	lib, err := st.CreateLibrary(context.Background(), "Audio", false, "disc-plugin", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	filter := store.LibraryFilter{AllowedIDs: []store.LibraryID{lib.ID}}
	prober := &pathProber{probes: map[string]ffmpeg.Probe{
		p1: {
			Container:       "m4b",
			AudioCodec:      "aac",
			DurationSeconds: 120,
			Chapters:        []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 120, Title: "Part 1"}},
		},
		p2: {
			Container:       "m4b",
			AudioCodec:      "aac",
			DurationSeconds: 180,
			Chapters:        []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 180, Title: "Part 2"}},
		},
	}}
	parsers := map[string]FilenameParser{"disc-plugin": discSingleFileParser{}}

	res, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	mediaID := res.Files[0].MediaID

	// Seed pre-fix state: stale Part 1 duration 120s (less than multi-part chapter end 300s)
	// AND a probe-owned grouped index field (parserOwnedProbe).
	if _, err := st.SetMultiPartMediaDuration(context.Background(), mediaID, 120); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshParserMediaFields(context.Background(), mediaID, store.ParserFieldRefresh{
		Authoritative: true,
		Fields: []store.MediaField{
			{Name: "class", Type: store.EntityFieldText, Text: "audio"},
			{Name: "index", Type: store.EntityFieldNumber, Number: 10002},
			{Name: "index-end", Type: store.EntityFieldNumber, Number: 10},
		},
		ProbeDependent: map[string]bool{"index": true, "index-end": true},
	}); err != nil {
		t.Fatal(err)
	}

	prober.calls = nil
	if _, err := Scan(context.Background(), st, prober, nil, parsers, nil, nil, Request{LibraryID: lib.ID, Root: root}); err != nil {
		t.Fatal(err)
	}
	healed, err := st.Media(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	if healed.DurationSeconds != 300 {
		t.Fatalf("healed DurationSeconds = %d, want 300 when chapter end exceeds stale duration", healed.DurationSeconds)
	}
	fields, err := st.MediaFields(context.Background(), mediaID, filter)
	if err != nil {
		t.Fatal(err)
	}
	mf := mediaFieldMap(fields)
	if _, hasIndex := mf["index"]; hasIndex {
		t.Fatalf("fast disc rescan left probe-owned index field: %+v", mf["index"])
	}
	if _, hasIndexEnd := mf["index-end"]; hasIndexEnd {
		t.Fatalf("fast disc rescan left probe-owned index-end field: %+v", mf["index-end"])
	}
}
