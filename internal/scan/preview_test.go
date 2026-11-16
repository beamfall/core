package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestPreviewRootsPlansChangesWithoutWriting is the CORE-1054 walk
// acceptance: a preview reports one add, one update and one missing file, the
// rest unchanged, and leaves the catalog and the library digest exactly as
// they were.
func TestPreviewRootsPlansChangesWithoutWriting(t *testing.T) {
	root, st, paths := seedScannedLibrary(t, 4)
	ctx := context.Background()
	added := writeScanFile(t, root, "clip-new.mp4", []byte("a new clip"))
	writeScanFile(t, root, filepath.Base(paths[1]), []byte("clip 0001 re-encoded at a longer length"))
	if err := os.Remove(paths[2]); err != nil {
		t.Fatal(err)
	}
	beforeLive := liveScanPaths(t, st)
	beforeDigest, err := LibraryStateDigest(ctx, st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}

	preview, err := PreviewRoots(ctx, st, "library-local", []string{root}, 0, false)
	if err != nil {
		t.Fatalf("PreviewRoots: %v", err)
	}
	if preview.Adds != 1 || preview.Updates != 1 || preview.Missing != 1 || preview.Unchanged != 2 {
		t.Fatalf("preview counts = %+v, want 1 add, 1 update, 1 missing, 2 unchanged", preview)
	}
	want := []PreviewChange{
		{Kind: PreviewChangeUpdate, Path: paths[1]},
		{Kind: PreviewChangeAdd, Path: added},
		{Kind: PreviewChangeMissing, Path: paths[2]},
	}
	if !slices.Equal(preview.Changes, want) || preview.Truncated || len(preview.MissingSkipped) != 0 {
		t.Fatalf("preview changes = %+v truncated=%v skipped=%v, want %+v", preview.Changes, preview.Truncated, preview.MissingSkipped, want)
	}
	if after := liveScanPaths(t, st); !slices.Equal(after, beforeLive) {
		t.Fatalf("live files after preview = %q, want unchanged %q", after, beforeLive)
	}
	afterDigest, err := LibraryStateDigest(ctx, st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		t.Fatal("preview moved the library state digest; it must write nothing")
	}
	jobs, err := st.ListJobs(ctx, store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("preview queued jobs %#v, want none", jobs)
	}
}

// TestPreviewRootsBoundsTheList keeps every change in the counts while the
// list stops at the limit and says so.
func TestPreviewRootsBoundsTheList(t *testing.T) {
	root, st, _ := seedScannedLibrary(t, 1)
	for _, name := range []string{"clip-a.mp4", "clip-b.mp4", "clip-c.mp4"} {
		writeScanFile(t, root, name, []byte(name))
	}
	preview, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Adds != 3 || len(preview.Changes) != 2 || !preview.Truncated {
		t.Fatalf("preview = %+v, want 3 adds, 2 listed, truncated", preview)
	}
}

// TestPreviewRootsAppliesMissingGuard counts no missing files for a root the
// scan's missing-ratio guard would skip, and names the reason.
func TestPreviewRootsAppliesMissingGuard(t *testing.T) {
	root, st, paths := seedScannedLibrary(t, missingRatioGuardMinPresent+10)
	for _, path := range paths[:len(paths)/2+1] {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Missing != 0 || len(preview.MissingSkipped) != 1 || !strings.Contains(preview.MissingSkipped[0], "missing-ratio guard") {
		t.Fatalf("preview = %+v, want no missing and the guard reason", preview)
	}
}

// TestPreviewRootsGuardCountsPlannedAdds matches the scan, which counts
// present files after its imports: deleting 11 of 20 files while adding 10
// stays under the guard (11 of 30), so both mark the 11 files missing.
func TestPreviewRootsGuardCountsPlannedAdds(t *testing.T) {
	root, st, paths := seedScannedLibrary(t, missingRatioGuardMinPresent)
	for _, path := range paths[:11] {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		writeScanFile(t, root, "added-"+zeroPad(i)+".mp4", []byte("added "+zeroPad(i)))
	}
	preview, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, MaxPreviewChangeLimit, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Adds != 10 || preview.Missing != 11 || len(preview.MissingSkipped) != 0 {
		t.Fatalf("preview = adds %d missing %d skipped %v, want 10 adds and 11 missing", preview.Adds, preview.Missing, preview.MissingSkipped)
	}
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatal(err)
	}
	if live := liveScanPaths(t, st); len(live) != 19 {
		t.Fatalf("live files after the scan = %d, want 19 (the scan agrees with the preview)", len(live))
	}
}

// TestPreviewRootsSkipsSettlingFiles leaves a file inside the stability
// window out, as the scan does.
func TestPreviewRootsSkipsSettlingFiles(t *testing.T) {
	root, st, _ := seedScannedLibrary(t, 1)
	fresh := filepath.Join(root, "clip-settling.mp4")
	if err := os.WriteFile(fresh, []byte("still copying"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Adds != 0 || preview.Unchanged != 1 {
		t.Fatalf("preview = %+v, want the settling file skipped", preview)
	}
}

// TestPreviewRootsFailsOnUnavailableRoot fails like the scan instead of
// planning every file missing.
func TestPreviewRootsFailsOnUnavailableRoot(t *testing.T) {
	root, st, _ := seedScannedLibrary(t, 2)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, 0, false); err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("PreviewRoots error = %v, want unavailable root", err)
	}
}

// TestLibraryStateDigestMovesWhenTheLibraryChanges covers what makes a
// preview stale: a scan that refreshes the catalog and a root change.
func TestLibraryStateDigestMovesWhenTheLibraryChanges(t *testing.T) {
	root, st, _ := seedScannedLibrary(t, 2)
	ctx := context.Background()
	first, err := LibraryStateDigest(ctx, st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err := LibraryStateDigest(ctx, st, "library-local", []string{root, t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if otherRoot == first {
		t.Fatal("digest ignores the roots")
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := Scan(ctx, st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatal(err)
	}
	rescanned, err := LibraryStateDigest(ctx, st, "library-local", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if rescanned == first {
		t.Fatal("digest did not move after a rescan refreshed the catalog")
	}
}

// TestPreviewRootsCountsSidecarNamedImagesByPluginClass is the CORE-2180
// acceptance for the preview: a "-thumb" image beside a video is an add for a
// plugin that declares the image class and is left out for a video-only one,
// matching what the scan imports.
func TestPreviewRootsCountsSidecarNamedImagesByPluginClass(t *testing.T) {
	root, st, _ := seedScannedLibrary(t, 1)
	video := writeScanFile(t, root, "IMG_1.mov", []byte("a short clip"))
	thumb := writeScanFile(t, root, "IMG_1-thumb.jpg", []byte("a still frame"))
	for _, tc := range []struct {
		name             string
		imagesAreContent bool
		want             []string
	}{
		{"image class", true, []string{video, thumb}},
		{"video only", false, []string{video}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preview, err := PreviewRoots(context.Background(), st, "library-local", []string{root}, 0, tc.imagesAreContent)
			if err != nil {
				t.Fatal(err)
			}
			var adds []string
			for _, change := range preview.Changes {
				if change.Kind == PreviewChangeAdd {
					adds = append(adds, change.Path)
				}
			}
			slices.Sort(adds)
			want := slices.Sorted(slices.Values(tc.want))
			if preview.Adds != len(want) || !slices.Equal(adds, want) {
				t.Fatalf("preview adds = %d %q, want %q", preview.Adds, adds, want)
			}
		})
	}
}
