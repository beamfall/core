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
	"github.com/beamfall/core/internal/libencrypt"
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

type failingSecondaryExtractor struct {
	err error
}

func (e failingSecondaryExtractor) ExtractLocal(_ context.Context, _, _, _ string) (LocalMetadata, error) {
	return LocalMetadata{}, e.err
}

type richLocalExtractor struct {
	dateByBase map[string]string
	albumTitle string
}

func (e *richLocalExtractor) ExtractLocal(_ context.Context, _, _, path string) (LocalMetadata, error) {
	date := e.dateByBase[filepath.Base(path)]
	md := LocalMetadata{
		Fields: map[string]any{},
		Entities: []LocalEntity{
			{Kind: "album", Title: e.albumTitle, Relation: "contains"},
		},
	}
	if date != "" {
		md.Fields["capture_date"] = date
	}
	return md, nil
}

// TestScanUpgradesGroupedLocalVideoClipsToStandalone checks that pre-existing
// multi-part and multi-version Media rows in a local-extractor library are
// split transactionally into standalone clips on upgrade, keeping collections
// while extracting each clip's own capture-date and duration.
func TestScanUpgradesGroupedLocalVideoClipsToStandalone(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	files := []string{"roll-pt1.mov", "roll-pt2.mov", "trip-1080p.mov", "trip-720p.mov"}
	for i, name := range files {
		writeScanFile(t, root, name, []byte("grouped legacy clip "+name+string(rune('0'+i))))
	}
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 11, HasVideo: true,
	}}
	parsers := map[string]FilenameParser{imageRuleFixturePlugin: groupableCameraParser{}}

	// Seed BEFORE local extractors are active so files group as Parts + Versions.
	initial, err := ScanWithLocalExtractors(ctx, st, prober, nil, parsers, nil, resolver, nil, req)
	if err != nil {
		t.Fatalf("legacy grouped scan: %v", err)
	}
	if initial.MediaImported != 2 || len(initial.Files) != 4 {
		t.Fatalf("legacy grouped scan imported=%d files=%d, want 2/4", initial.MediaImported, len(initial.Files))
	}

	col, err := st.CreateCollection(ctx, store.Collection{LibraryID: libraryID, Name: "Camera Roll"}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	seedFiles, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	seenInitial := map[store.MediaID]bool{}
	var groupedIDs []store.MediaID
	for _, f := range seedFiles {
		if !seenInitial[f.MediaID] {
			seenInitial[f.MediaID] = true
			groupedIDs = append(groupedIDs, f.MediaID)
		}
	}
	if _, err := st.AddCollectionMedia(ctx, col.ID, groupedIDs, store.LibraryFilter{AllLibraries: true}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	d1 := "2025-05-01T10:00:00Z"
	d2 := "2025-05-02T11:00:00Z"
	d3 := "2025-05-03T12:00:00Z"
	d4 := "2025-05-04T13:00:00Z"
	extractors := map[string][]LocalExtractor{
		imageRuleFixturePlugin: {&richLocalExtractor{
			albumTitle: "Spring Roll",
			dateByBase: map[string]string{
				"roll-pt1.mov":   d1,
				"roll-pt2.mov":   d2,
				"trip-1080p.mov": d3,
				"trip-720p.mov":  d4,
			},
		}},
	}

	// Fast upgrade scan must split all 4 clips, preserve collection links,
	// re-probe each clip individually, and extract distinct capture-dates.
	upgraded, err := ScanWithLocalExtractors(ctx, st, prober, nil, parsers, nil, resolver, extractors, req)
	if err != nil {
		t.Fatalf("upgrade scan: %v", err)
	}
	if len(upgraded.Files) != 4 {
		t.Fatalf("upgrade scan files = %d, want 4", len(upgraded.Files))
	}
	wantDates := map[string]string{
		"roll-pt1.mov":   d1,
		"roll-pt2.mov":   d2,
		"trip-1080p.mov": d3,
		"trip-720p.mov":  d4,
	}
	upgradedFiles, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(upgradedFiles) != 4 {
		t.Fatalf("upgraded media files = %d, want 4", len(upgradedFiles))
	}
	members, err := st.CollectionMemberMediaIDs(ctx, col.ID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 {
		t.Fatalf("collection members after split = %d, want 4", len(members))
	}
	seenIDs := map[store.MediaID]bool{}
	for _, f := range upgradedFiles {
		if seenIDs[f.MediaID] {
			t.Fatalf("file %s still grouped under %s", f.Path, f.MediaID)
		}
		seenIDs[f.MediaID] = true
		fields, err := st.MediaFields(ctx, f.MediaID, store.LibraryFilter{AllLibraries: true})
		if err != nil {
			t.Fatal(err)
		}
		wantDate, _ := time.Parse(time.RFC3339, wantDates[filepath.Base(f.Path)])
		if got := mediaFieldMap(fields)["capture-date"].Date; !got.Equal(wantDate) {
			t.Fatalf("%s capture-date = %v, want %v", filepath.Base(f.Path), got, wantDate)
		}
	}
}

// TestScanMultiExtractorPartialFailureLeavesNoPartialWrites checks that when
// one local extractor succeeds and a second errors, no partial fields,
// entities, or completion markers are written so the next scan retries cleanly.
func TestScanMultiExtractorPartialFailureLeavesNoPartialWrites(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeScanFile(t, root, "clip.mov", []byte("partial extractor clip"))
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 9, HasVideo: true,
	}}

	good := &richLocalExtractor{
		albumTitle: "Trip",
		dateByBase: map[string]string{"clip.mov": "2025-06-15T09:30:00Z"},
	}
	failing := failingSecondaryExtractor{err: errors.New("exiftool crashed")}
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, map[string][]LocalExtractor{
		imageRuleFixturePlugin: {good, failing},
	}, req); err != nil {
		t.Fatalf("partial scan: %v", err)
	}

	files, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %+v (%v)", files, err)
	}
	mediaID := files[0].MediaID
	if fields, err := st.MediaFields(ctx, mediaID, store.LibraryFilter{AllLibraries: true}); err != nil || len(fields) != 0 {
		t.Fatalf("partial scan left fields %+v (%v), want empty", fields, err)
	}
	if entities, err := st.EntitiesForMedia(ctx, mediaID, "", store.LibraryFilter{AllLibraries: true}); err != nil || len(entities) != 0 {
		t.Fatalf("partial scan left entities %+v (%v), want empty", entities, err)
	}

	// Clean retry with failing extractor fixed must persist both fields & entity once.
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, map[string][]LocalExtractor{
		imageRuleFixturePlugin: {good},
	}, req); err != nil {
		t.Fatalf("retry scan: %v", err)
	}
	if fields, err := st.MediaFields(ctx, mediaID, store.LibraryFilter{AllLibraries: true}); err != nil || len(fields) != 1 {
		t.Fatalf("retry fields = %+v (%v), want 1", fields, err)
	}
	if entities, err := st.EntitiesForMedia(ctx, mediaID, "", store.LibraryFilter{AllLibraries: true}); err != nil || len(entities) != 1 {
		t.Fatalf("retry entities = %+v (%v), want 1", entities, err)
	}
}

// TestScanRepeatedScansWithVerifiedByteCopiesStayStandaloneWithoutChurn checks
// that identical-byte legacy SHA-256 and sample copies in a local-extractor
// library stay standalone across repeated scans without mergeVerifiedCopies
// re-merging and re-splitting them.
func TestScanRepeatedScansWithVerifiedByteCopiesStayStandaloneWithoutChurn(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	sharedBytes := []byte("identical camera roll video clip bytes across copies")
	aPath := writeScanFile(t, root, "copy-a.mov", sharedBytes)
	bPath := writeScanFile(t, root, "copy-b.mov", sharedBytes)
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 10, HasVideo: true,
	}}
	extractor := &toggleLocalExtractor{date: "2025-10-01T12:00:00Z"}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}

	// Seed copy-a as a legacy sha256 fingerprint copy alongside copy-b's sample1
	// fingerprint to exercise VersionCopyCandidates + mergeVerifiedCopies on scan.
	legacyDigest, err := store.HashMediaFileContentContext(ctx, aPath)
	if err != nil {
		t.Fatalf("hash copy-a: %v", err)
	}
	osHash, err := OSHash(aPath)
	if err != nil {
		t.Fatalf("oshash copy-a: %v", err)
	}
	aInfo, err := os.Stat(aPath)
	if err != nil {
		t.Fatalf("stat copy-a: %v", err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           libraryID,
		Path:                aPath,
		Title:               "copy-a",
		Container:           "mov",
		Codec:               "hevc",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  legacyDigest,
		DurationSecondsHint: 10,
		VideoWidthHint:      1920,
		VideoHeightHint:     1080,
		OSHash:              osHash,
		SizeBytes:           aInfo.Size(),
		ModTime:             aInfo.ModTime().UTC(),
	}); err != nil {
		t.Fatalf("seed legacy copy-a: %v", err)
	}

	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	files1, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil || len(files1) != 2 {
		t.Fatalf("scan 1 files = %+v, %v", files1, err)
	}
	idByPath1 := map[string]store.MediaID{}
	for _, f := range files1 {
		idByPath1[f.Path] = f.MediaID
	}
	if idByPath1[aPath] == idByPath1[bPath] {
		t.Fatalf("duplicate copies merged on scan 1: %v", idByPath1)
	}

	// Subsequent unchanged scans must keep the exact same 2 MediaIDs without re-probing.
	for _, round := range []string{"scan 2", "scan 3"} {
		if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
			t.Fatalf("%s: %v", round, err)
		}
		files, err := st.MediaFilesForLibrary(ctx, libraryID)
		if err != nil || len(files) != 2 {
			t.Fatalf("%s files = %+v, %v", round, files, err)
		}
		for _, f := range files {
			if f.MediaID != idByPath1[f.Path] {
				t.Fatalf("%s churned MediaID for %s: got %s, want %s", round, f.Path, f.MediaID, idByPath1[f.Path])
			}
		}
	}
	if prober.calls != 2 || len(extractor.calls) != 2 {
		t.Fatalf("unchanged copy rescans re-ran work: probes=%d calls=%d, want 2/2", prober.calls, len(extractor.calls))
	}
}

// TestScanUnlockedEncryptedLibraryExtractsClipOnce checks that unchanged clips in
// an unsealed encrypted library hit the library-scoped fast path and skip repeat
// probing and local extraction once the completion marker commits.
func TestScanUnlockedEncryptedLibraryExtractsClipOnce(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st, err := store.Open(ctx, dataDir, store.Options{
		SeedDevelopmentData:               true,
		EnableEncryptedLibraryDBLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	cipher := scanCatalogCipher{libencrypt.NewManagerWithParams(scanSecrets{}, libencrypt.TestArgon2Params)}
	if _, err := cipher.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	writeScanFile(t, root, "secret-clip.mov", []byte("encrypted clip bytes"))
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 14, HasVideo: true,
	}}
	extractor := &toggleLocalExtractor{}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}

	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("encrypted scan 1: %v", err)
	}
	if prober.calls != 1 || len(extractor.calls) != 1 {
		t.Fatalf("encrypted scan 1 probes=%d calls=%d, want 1/1", prober.calls, len(extractor.calls))
	}

	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("encrypted scan 2: %v", err)
	}
	if prober.calls != 1 || len(extractor.calls) != 1 {
		t.Fatalf("encrypted rescan repeated work: probes=%d calls=%d, want 1/1", prober.calls, len(extractor.calls))
	}
}

// TestScanEncryptedLegacySHA256FilePreservesMediaID checks CORE-3029/CORE-3028:
// an unchanged legacy sha256: file in an encrypted library retains its legacy
// fingerprint and MediaID across scans while promoting to the fast path after verification.
func TestScanEncryptedLegacySHA256FilePreservesMediaID(t *testing.T) {
	ctx := t.Context()
	dataDir := storetest.MigratedDataDir(t)
	st, err := store.Open(ctx, dataDir, store.Options{
		SeedDevelopmentData:               true,
		EnableEncryptedLibraryDBLifecycle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	if err := st.SetLibraryBacking(ctx, libraryID, false, true); err != nil {
		t.Fatal(err)
	}
	cipher := scanCatalogCipher{libencrypt.NewManagerWithParams(scanSecrets{}, libencrypt.TestArgon2Params)}
	if _, err := cipher.Provision(string(libraryID), libencrypt.UnsealModeAuto, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.UnsealEncryptedLibraryDB(ctx, libraryID, cipher); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	clipPath := writeScanFile(t, root, "legacy-secret.mov", []byte("encrypted legacy sha256 clip bytes"))
	legacyDigest, err := store.HashMediaFileContentContext(ctx, clipPath)
	if err != nil {
		t.Fatalf("hash legacy clip: %v", err)
	}
	osHash, err := OSHash(clipPath)
	if err != nil {
		t.Fatalf("oshash legacy clip: %v", err)
	}
	info, err := os.Stat(clipPath)
	if err != nil {
		t.Fatal(err)
	}
	seededMedia, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID:           libraryID,
		Path:                clipPath,
		Title:               "legacy-secret",
		Container:           "mov",
		Codec:               "hevc",
		Class:               store.MediaClassVideo,
		ContentFingerprint:  legacyDigest,
		DurationSecondsHint: 8,
		OSHash:              osHash,
		SizeBytes:           info.Size(),
		ModTime:             info.ModTime().UTC(),
	})
	if err != nil {
		t.Fatalf("seed encrypted legacy clip: %v", err)
	}

	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 8, HasVideo: true,
	}}
	extractor := &toggleLocalExtractor{date: "2025-11-01T12:00:00Z"}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}

	// Scan 1 verifies bytes, keeps sha256: identity and MediaID, and backfills extraction once.
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("encrypted legacy scan 1: %v", err)
	}
	files1, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil || len(files1) != 1 {
		t.Fatalf("scan 1 files = %+v, %v", files1, err)
	}
	if files1[0].MediaID != seededMedia.ID || files1[0].ContentFingerprint != legacyDigest {
		t.Fatalf("encrypted legacy scan 1 replaced identity: got (%s, %s), want (%s, %s)",
			files1[0].MediaID, files1[0].ContentFingerprint, seededMedia.ID, legacyDigest)
	}
	if prober.calls != 1 || len(extractor.calls) != 1 {
		t.Fatalf("encrypted legacy scan 1 probes=%d calls=%d, want 1/1", prober.calls, len(extractor.calls))
	}

	// Scan 2 verifies bytes, retains sha256: + MediaID, and skips re-probing/extraction.
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("encrypted legacy scan 2: %v", err)
	}
	if prober.calls != 1 || len(extractor.calls) != 1 {
		t.Fatalf("encrypted legacy scan 2 repeated work: probes=%d calls=%d, want 1/1", prober.calls, len(extractor.calls))
	}
}

// TestScanZeroDurationClipDoesNotRepeatLocalExtraction checks that sub-half-second
// clips (probed duration 0) still probe for duration recovery without re-running
// local extractors once completed.
func TestScanZeroDurationClipDoesNotRepeatLocalExtraction(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	writeScanFile(t, root, "tiny.mov", []byte("micro video clip bytes"))
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 0, HasVideo: true,
	}}
	extractor := &toggleLocalExtractor{date: "2025-11-05T12:00:00Z"}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}

	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if len(extractor.calls) != 1 {
		t.Fatalf("scan 1 extractor calls = %d, want 1", len(extractor.calls))
	}
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	if len(extractor.calls) != 1 {
		t.Fatalf("duration-0 clip re-ran extraction on fast path: calls=%d, want 1", len(extractor.calls))
	}
}

// TestScanUnstableLegacyBackfillClipSkipsLocalExtraction verifies that a legacy
// backfill video whose file sample fails verification on rescan does not fall
// back to running local extractors or marking the unverified fingerprint extracted.
func TestScanUnstableLegacyBackfillClipSkipsLocalExtraction(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	clip := writeScanFile(t, root, "clip.mov", []byte("unstable legacy video clip bytes"))
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 3, HasVideo: true,
	}}

	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, nil, req); err != nil {
		t.Fatalf("initial legacy scan: %v", err)
	}
	missing, err := st.LibraryFilePathsWithoutLocalExtraction(ctx, libraryID, store.MediaClassVideo)
	if err != nil {
		t.Fatalf("LibraryFilePathsWithoutLocalExtraction before: %v", err)
	}
	if _, ok := missing[clip]; !ok {
		t.Fatalf("expected %q in backfill set before extractor scan, got %v", clip, missing)
	}

	// Shift mtime back (still past the stability window) so rescan misses the
	// stat fast path and must verify the file sample before extracting.
	info, err := os.Stat(clip)
	if err != nil {
		t.Fatal(err)
	}
	bumped := info.ModTime().Add(-time.Minute)
	if err := os.Chtimes(clip, bumped, bumped); err != nil {
		t.Fatal(err)
	}

	origSample := sampleScanFile
	t.Cleanup(func() { sampleScanFile = origSample })
	sampleScanFile = func(context.Context, string, store.SampleFingerprintCheck) (string, error) {
		return "", errors.New("unstable sample")
	}

	probesBefore := prober.calls
	extractor := &toggleLocalExtractor{date: "2025-11-05T12:00:00Z"}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("backfill scan under unstable sample: %v", err)
	}
	if prober.calls != probesBefore {
		t.Fatalf("prober ran for unverified backfill clip: calls=%d, want %d", prober.calls, probesBefore)
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("extractor ran for unverified backfill clip: calls=%v, want 0", extractor.calls)
	}
	missingAfter, err := st.LibraryFilePathsWithoutLocalExtraction(ctx, libraryID, store.MediaClassVideo)
	if err != nil {
		t.Fatalf("LibraryFilePathsWithoutLocalExtraction after: %v", err)
	}
	if _, ok := missingAfter[clip]; !ok {
		t.Fatalf("unverified clip %q dropped from backfill set: %v", clip, missingAfter)
	}
}

func TestScanFastPathProbedClipImportsEmbeddedChapters(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	clip := writeScanFile(t, root, "chapters.mov", []byte("chaptered clip bytes"))
	st := storetest.Open(t)
	libraryID, resolver := imageRuleLibrary(t, st, []store.MediaClass{store.MediaClassImage, store.MediaClassVideo})
	req := Request{LibraryID: libraryID, Root: root}

	if _, err := ScanWithLocalExtractors(ctx, st, nil, nil, nil, nil, resolver, nil, req); err != nil {
		t.Fatalf("initial scan without probe: %v", err)
	}
	prober := &countingProber{probe: ffmpeg.Probe{
		Container: "mov", VideoCodec: "hevc", DurationSeconds: 30, HasVideo: true,
		Chapters: []ffmpeg.ProbeChapter{{StartSeconds: 0, EndSeconds: 15, Title: "Intro"}},
	}}
	extractor := &toggleLocalExtractor{date: "2025-11-05T12:00:00Z"}
	extractors := map[string][]LocalExtractor{imageRuleFixturePlugin: {extractor}}
	if _, err := ScanWithLocalExtractors(ctx, st, prober, nil, nil, nil, resolver, extractors, req); err != nil {
		t.Fatalf("fast-path probe scan: %v", err)
	}
	files, err := st.MediaFilesForLibrary(ctx, libraryID)
	if err != nil || len(files) != 1 || files[0].Path != clip {
		t.Fatalf("MediaFilesForLibrary = (%+v, %v)", files, err)
	}
	markers, err := st.MarkersForMedia(ctx, files[0].MediaID, store.LibraryFilter{AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(markers) != 1 || markers[0].Method != "chapter-import" || markers[0].Title != "Intro" {
		t.Fatalf("fast-path probed clip markers = %+v, want 1 chapter-import Intro", markers)
	}
}
