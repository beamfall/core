package scan

import (
	"context"
	"path/filepath"
	"testing"
)

// deferrableCount looks up one kind's estimated count in an Estimate's
// breakdown, failing the test if the kind is absent.
func deferrableCount(t *testing.T, est Estimate, kind string) DeferrableWork {
	t.Helper()
	for _, w := range est.DeferrableWork {
		if w.Kind == kind {
			return w
		}
	}
	t.Fatalf("deferrable kind %q missing from estimate %#v", kind, est)
	return DeferrableWork{}
}

// TestEstimateSplitsInlineFromDeferrable is the OPS-4 core acceptance: for a
// fixture directory the estimate distinguishes the inline, playability-gating
// scan work from the deferrable background asset backlog — the "play now;
// sprites overnight" framing made machine-readable. Every deferrable entry must
// report PlayabilityGating=false, and the inline work must report gating=true.
func TestEstimateSplitsInlineFromDeferrable(t *testing.T) {
	// feature:first-scan-estimate
	root := t.TempDir()
	writeScanFile(t, root, "movie.mkv", []byte("video bytes"))
	writeScanFile(t, root, "show/ep1.mp4", []byte("more video"))
	writeScanFile(t, root, "music/song.flac", []byte("audio bytes"))
	// Junk and Extras are excluded exactly as the real scan excludes them, so
	// the estimate counts what would import, not raw file count.
	writeScanFile(t, root, "notes.txt", []byte("not media"))
	writeScanFile(t, root, ".DS_Store", []byte("junk"))
	writeScanFile(t, root, "Extras/trailer.mkv", []byte("an extra"))

	est, err := EstimateRoots(context.Background(), []string{root})
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}

	if est.EstimatedFiles != 3 {
		t.Fatalf("EstimatedFiles = %d, want 3 (2 video + 1 audio; .txt/.DS_Store/Extra excluded)", est.EstimatedFiles)
	}
	if est.EstimatedVideoFiles != 2 || est.EstimatedAudioFiles != 1 {
		t.Fatalf("video/audio split = %d/%d, want 2/1", est.EstimatedVideoFiles, est.EstimatedAudioFiles)
	}

	// Inline work gates playability; it is the must-do-now scan.
	if !est.InlineWorkGatesPlayability {
		t.Fatal("InlineWorkGatesPlayability = false, want true — the inline scan IS the playability-gating work")
	}
	if est.EstimatedInlineSeconds <= 0 {
		t.Fatalf("EstimatedInlineSeconds = %d, want > 0 for 3 files", est.EstimatedInlineSeconds)
	}

	// Every deferrable asset kind is non-gating ("sprites overnight").
	if len(est.DeferrableWork) == 0 {
		t.Fatal("DeferrableWork is empty, want the Generated Asset family breakdown")
	}
	for _, w := range est.DeferrableWork {
		if w.PlayabilityGating {
			t.Errorf("deferrable kind %q reports PlayabilityGating=true, want false — Generated Assets never gate playback", w.Kind)
		}
	}

	// The per-kind counts follow the cheap class split: video grows sprites,
	// preview clips, and Marker thumbnails; audio grows waveform envelopes.
	if c := deferrableCount(t, est, DeferrableTrickplay); c.EstimatedCount != 2 {
		t.Errorf("%s count = %d, want 2 (video files)", DeferrableTrickplay, c.EstimatedCount)
	}
	if c := deferrableCount(t, est, DeferrablePreviewClip); c.EstimatedCount != 2 {
		t.Errorf("%s count = %d, want 2 (video files)", DeferrablePreviewClip, c.EstimatedCount)
	}
	if c := deferrableCount(t, est, DeferrableMarkerThumbs); c.EstimatedCount != 2 {
		t.Errorf("%s count = %d, want 2 (video files)", DeferrableMarkerThumbs, c.EstimatedCount)
	}
	if c := deferrableCount(t, est, DeferrableWaveform); c.EstimatedCount != 1 {
		t.Errorf("%s count = %d, want 1 (audio file)", DeferrableWaveform, c.EstimatedCount)
	}
	if c := deferrableCount(t, est, DeferrableSilenceMap); c.EstimatedCount != 1 {
		t.Errorf("%s count = %d, want 1 (audio file)", DeferrableSilenceMap, c.EstimatedCount)
	}
}

// TestEstimateScalesWithFileCount asserts the inline-duration estimate grows
// with the number of files — more files means a longer must-do-now scan and a
// larger deferrable backlog.
func TestEstimateScalesWithFileCount(t *testing.T) {
	// feature:first-scan-estimate
	small := t.TempDir()
	writeScanFile(t, small, "a.mp4", []byte("one"))

	large := t.TempDir()
	for _, name := range []string{"a.mp4", "b.mkv", "c.mov", "d.mp4", "e.webm"} {
		writeScanFile(t, large, name, []byte(name))
	}

	smallEst, err := EstimateRoots(context.Background(), []string{small})
	if err != nil {
		t.Fatalf("EstimateRoots(small): %v", err)
	}
	largeEst, err := EstimateRoots(context.Background(), []string{large})
	if err != nil {
		t.Fatalf("EstimateRoots(large): %v", err)
	}

	if largeEst.EstimatedFiles != 5 || smallEst.EstimatedFiles != 1 {
		t.Fatalf("file counts = %d (large) / %d (small), want 5 / 1", largeEst.EstimatedFiles, smallEst.EstimatedFiles)
	}
	if largeEst.EstimatedInlineSeconds <= smallEst.EstimatedInlineSeconds {
		t.Errorf("inline seconds did not scale: large=%d small=%d, want large > small",
			largeEst.EstimatedInlineSeconds, smallEst.EstimatedInlineSeconds)
	}
	if c := deferrableCount(t, largeEst, DeferrableTrickplay); c.EstimatedCount != 5 {
		t.Errorf("large %s count = %d, want 5", DeferrableTrickplay, c.EstimatedCount)
	}
}

// TestEstimateMultipleRootsAggregate asserts the estimate sums across the
// Library's roots (a Library may have several), and that a non-existent root
// contributes zero rather than erroring (the wizard may probe a path the
// operator is still typing).
func TestEstimateMultipleRootsAggregate(t *testing.T) {
	// feature:first-scan-estimate
	rootA := t.TempDir()
	writeScanFile(t, rootA, "a.mp4", []byte("a"))
	rootB := t.TempDir()
	writeScanFile(t, rootB, "b.flac", []byte("b"))
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	est, err := EstimateRoots(context.Background(), []string{rootA, rootB, missing})
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}
	if est.EstimatedFiles != 2 || est.EstimatedVideoFiles != 1 || est.EstimatedAudioFiles != 1 {
		t.Fatalf("aggregate = %d files (%dv/%da), want 2 (1v/1a) — missing root must contribute zero, not error",
			est.EstimatedFiles, est.EstimatedVideoFiles, est.EstimatedAudioFiles)
	}
}

// TestEstimateEmptyRootYieldsStableShape asserts an empty or nonexistent tree
// estimates zero files with a full, non-nil deferrable breakdown (zero counts)
// — the wizard renders a stable shape, never a null.
func TestEstimateEmptyRootYieldsStableShape(t *testing.T) {
	// feature:first-scan-estimate
	est, err := EstimateRoots(context.Background(), []string{t.TempDir()})
	if err != nil {
		t.Fatalf("EstimateRoots: %v", err)
	}
	if est.EstimatedFiles != 0 || est.EstimatedInlineSeconds != 0 {
		t.Fatalf("empty estimate = %d files / %ds, want 0/0", est.EstimatedFiles, est.EstimatedInlineSeconds)
	}
	if len(est.DeferrableWork) != 5 {
		t.Fatalf("DeferrableWork len = %d, want 5 zero-count entries (stable shape)", len(est.DeferrableWork))
	}
	if !est.InlineWorkGatesPlayability {
		t.Error("InlineWorkGatesPlayability = false on empty tree, want true (invariant of the split)")
	}
}
