package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type toggleLocalExtractor struct {
	err   error
	date  string
	calls []string
}

func (e *toggleLocalExtractor) ExtractLocal(_ context.Context, class, _, path string) (LocalMetadata, error) {
	e.calls = append(e.calls, class+"|"+filepath.Base(path))
	if e.err != nil {
		return LocalMetadata{}, e.err
	}
	if e.date == "" {
		return LocalMetadata{}, nil
	}
	return LocalMetadata{Fields: map[string]any{"capture_date": e.date}}, nil
}

// TestScanFastPathBackfillsUnextractedVideoClipsOnce checks CORE-3028:
// unchanged video clips lacking extracted local metadata/capture-date are probed
// and offered to local extractors on the fast path once, and are not re-probed
// on subsequent unchanged scans (even when the clip has no embedded capture
// date), while re-running when the file changes or a prior extraction errored.
func TestScanFastPathBackfillsUnextractedVideoClipsOnce(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	clip := writeScanFile(t, root, "clip.mov", []byte("camera roll clip bytes"))
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}

	// Seed with prober (so DurationSeconds is already populated) and no extractors,
	// isolating local-extraction backfill on the next fast-path scan.
	prober := &countingProber{probe: ffmpeg.Probe{
		Container:       "mov",
		VideoCodec:      "hevc",
		DurationSeconds: 12,
		HasVideo:        true,
	}}
	res, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, nil, req)
	if err != nil || len(res.Files) != 1 || prober.calls != 1 {
		t.Fatalf("seed scan = %+v, probes = %d, err = %v", res, prober.calls, err)
	}
	mediaID := res.Files[0].MediaID

	// Scan 2: unchanged clip with local extractor returning transient error ->
	// must probe & attempt extraction, and NOT mark attempted.
	captured := time.Date(2025, 7, 4, 15, 0, 0, 0, time.UTC)
	extractor := &toggleLocalExtractor{err: errors.New("temporary tool error")}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("error retry scan: %v", err)
	}
	if prober.calls != 2 || !slices.Equal(extractor.calls, []string{"video|clip.mov"}) {
		t.Fatalf("after failed extractor: probes=%d, extractor calls=%v; want 2 probes, 1 video call", prober.calls, extractor.calls)
	}

	// Scan 3: extractor now succeeds cleanly with no capture_date -> probes once
	// and marks attempted so subsequent unchanged scans skip probing.
	extractor.err = nil
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("clean empty extraction scan: %v", err)
	}
	if prober.calls != 3 || len(extractor.calls) != 2 {
		t.Fatalf("after clean empty extractor: probes=%d, calls=%d; want 3, 2", prober.calls, len(extractor.calls))
	}
	if prov, err := st.MediaFieldProvenance(ctx, mediaID, localAttemptMarkerField); err != nil || prov != store.MutationSourceScan {
		t.Fatalf("provenance after clean empty extraction = %q (%v), want scan", prov, err)
	}

	// Scan 4: unchanged clip with no capture-date must NOT be re-probed or re-extracted.
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("steady rescan: %v", err)
	}
	if prober.calls != 3 || len(extractor.calls) != 2 {
		t.Fatalf("unchanged rescan re-probed empty clip: probes=%d, calls=%d; want 3, 2", prober.calls, len(extractor.calls))
	}

	// File changes on disk and now carries capture_date -> full path probes and extracts it.
	if err := os.WriteFile(clip, []byte("camera roll clip updated"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-20 * time.Minute)
	if err := os.Chtimes(clip, past, past); err != nil {
		t.Fatal(err)
	}
	extractor.date = captured.Format(time.RFC3339)
	res, err = ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req)
	if err != nil {
		t.Fatalf("changed clip scan: %v", err)
	}
	if prober.calls != 4 || len(extractor.calls) != 3 || len(res.Files) != 1 {
		t.Fatalf("changed clip scan: probes=%d, calls=%d, files=%d; want 4, 3, 1", prober.calls, len(extractor.calls), len(res.Files))
	}
	fields, err := st.MediaFields(ctx, res.Files[0].MediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := mediaFieldMap(fields)["capture-date"].Date; !got.Equal(captured) {
		t.Fatalf("capture-date after edit = %v, want %v", got, captured)
	}

	// Subsequent unchanged rescan probes nothing and leaves capture-date intact.
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("post-edit steady rescan: %v", err)
	}
	if prober.calls != 4 || len(extractor.calls) != 3 {
		t.Fatalf("post-edit steady rescan re-probed: probes=%d, calls=%d; want 4, 3", prober.calls, len(extractor.calls))
	}
}

type groupableCameraParser struct{}

func (groupableCameraParser) Parse(path string, _ ParseProbe) (ParsedMedia, bool) {
	switch filepath.Base(path) {
	case "roll-pt1.mov":
		return ParsedMedia{Title: "Roll", Class: "video", PartGroup: "roll", Index: 1}, true
	case "roll-pt2.mov":
		return ParsedMedia{Title: "Roll", Class: "video", PartGroup: "roll", Index: 2}, true
	case "trip-1080p.mov":
		return ParsedMedia{Title: "Trip", Class: "video", VersionLabel: "1080p"}, true
	case "trip-720p.mov":
		return ParsedMedia{Title: "Trip", Class: "video", VersionLabel: "720p"}, true
	case "dup-a.mov", "dup-b.mov":
		return ParsedMedia{Title: "Dup", Class: "video"}, true
	default:
		return ParsedMedia{}, false
	}
}

type pathSelectiveLocalExtractor struct {
	empty map[string]bool
	date  string
}

func (e *pathSelectiveLocalExtractor) ExtractLocal(_ context.Context, _, _, path string) (LocalMetadata, error) {
	if e.empty[filepath.Base(path)] {
		return LocalMetadata{}, nil
	}
	return LocalMetadata{Fields: map[string]any{"capture_date": e.date}}, nil
}

// TestScanLocalExtractorVideoGroupingParityAcrossPaths checks CORE-3029:
// libraries with local extractors never group videos as parts or versions across
// either full or fast scan paths, even when an extractor yields no metadata.
func TestScanLocalExtractorVideoGroupingParityAcrossPaths(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	files := []string{
		"roll-pt1.mov", "roll-pt2.mov",
		"trip-1080p.mov", "trip-720p.mov",
		"dup-a.mov", "dup-b.mov",
	}
	for i, name := range files {
		writeScanFile(t, root, name, []byte("camera clip "+name+string(rune('0'+i))))
	}
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 15, HasVideo: true,
	}}
	parsers := map[string]FilenameParser{imageRuleFixturePlugin: groupableCameraParser{}}
	extractor := &pathSelectiveLocalExtractor{
		// Omit metadata on one member of each group to ensure standalone treatment
		// never depends on per-file extractor output.
		empty: map[string]bool{"roll-pt2.mov": true, "trip-720p.mov": true, "dup-b.mov": true},
		date:  "2025-08-01T10:00:00Z",
	}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}

	distinctMedia := func() int {
		mediaFiles, err := st.MediaFilesForLibrary(ctx, libraryID)
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[store.MediaID]bool, len(mediaFiles))
		for _, f := range mediaFiles {
			seen[f.MediaID] = true
		}
		return len(seen)
	}

	first, err := ScanWithLocalExtractors(ctx, st, prober, nil, parsers, nil, resolver, extractors, req)
	if err != nil {
		t.Fatalf("full scan: %v", err)
	}
	if first.MediaImported != len(files) || len(first.Files) != len(files) {
		t.Fatalf("full scan imported %d media / %d files, want %d", first.MediaImported, len(first.Files), len(files))
	}
	if got := distinctMedia(); got != len(files) {
		t.Fatalf("after full scan media count = %d, want %d standalone items", got, len(files))
	}

	// Unchanged fast rescan (zero probes, zero local extractor calls) must keep
	// all 6 items standalone and never merge split versions.
	second, err := ScanWithLocalExtractors(ctx, st, prober, nil, parsers, nil, resolver, extractors, req)
	if err != nil {
		t.Fatalf("fast rescan: %v", err)
	}
	if second.MediaImported != len(files) || prober.calls != len(files) {
		t.Fatalf("fast rescan imported=%d probes=%d, want %d imported and %d total probes",
			second.MediaImported, prober.calls, len(files), len(files))
	}
	if got := distinctMedia(); got != len(files) {
		t.Fatalf("after fast rescan media count = %d, want %d", got, len(files))
	}
}
