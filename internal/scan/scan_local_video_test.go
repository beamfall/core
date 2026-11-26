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
