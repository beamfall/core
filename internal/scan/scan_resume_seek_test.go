package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestSubtreeBeforeCheckpoint(t *testing.T) {
	root := filepath.FromSlash("/lib")
	last := filepath.FromSlash("/lib/b/s2/e3.mkv")
	cases := []struct {
		dir  string
		want bool
	}{
		{"/lib", false},      // root: walked
		{"/lib/a", true},     // sibling before the chain
		{"/lib/b", false},    // ancestor of the checkpoint
		{"/lib/b/s1", true},  // earlier season under the ancestor
		{"/lib/b/s2", false}, // the checkpoint's own folder
		{"/lib/b/s3", false}, // later season
		{"/lib/b-c", false},  // sorts below "b/" bytewise, after b/ in walk order
		{"/lib/a.b", true},   // '.' sorts below 'b' as a component
		{"/lib/b/s2/e3.mkv", false},
		{"/lib/c", false},
		{"/elsewhere/a", false}, // outside root: never pruned
	}
	for _, tc := range cases {
		if got := subtreeBeforeCheckpoint(root, filepath.FromSlash(tc.dir), last); got != tc.want {
			t.Errorf("subtreeBeforeCheckpoint(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

// TestScanResumeDoesNotReadFoldersBeforeCheckpoint is the CORE-2353 resume
// seek: a resumed walk lists only the checkpoint's ancestor chain, never a
// folder whose files all sort at or before the checkpoint, and visits the same
// files after it as a full walk does. The folders before the checkpoint are
// made unreadable, so any listing of one would surface as a skipped-file
// warning and a changed result.
func TestScanResumeDoesNotReadFoldersBeforeCheckpoint(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			names := []string{
				"a/s1/e1.mkv", "a/s1/e2.mkv", "a.b/x.mkv",
				"b/s1/e1.mkv", "b/s1/e2.mkv", "b/s2/e1.mkv", "b/s2/e2.mkv", "b/s2/e3.mkv", "b/s3/e1.mkv", "b/z.mkv",
				"b-c/y.mkv", "c/e1.mkv",
			}
			for _, name := range names {
				writeScanFile(t, root, name, []byte("bytes of "+name))
			}
			st := storetest.Open(t)
			req := Request{LibraryID: "library-local", Root: root}
			if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, req); err != nil {
				t.Fatalf("seed scan: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
			scanStarted := time.Now().UTC()
			gov := fixedGovernor(workers)

			// A full walk, for the order the resumed walk must reproduce.
			var full []string
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, gov, nil, req, jobCheckpoint{Version: 1, Stage: "scanning"}, scanStarted, func(checkpoint jobCheckpoint, _ store.JobProgress) error {
				full = append(full, checkpoint.LastPath)
				return nil
			}); err != nil {
				t.Fatalf("full walk: %v", err)
			}
			lastPath := filepath.Join(root, "b", "s2", "e2.mkv")
			at := slices.Index(full, lastPath)
			if at < 0 {
				t.Fatalf("full walk %q never reached %q", full, lastPath)
			}

			for _, dir := range []string{"a", "a.b", filepath.Join("b", "s1")} {
				path := filepath.Join(root, dir)
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o700) }) //nolint:gosec // G302: restores owner access so t.TempDir can be removed.
			}
			logs := captureScanWarnings(t)
			checkpoint := jobCheckpoint{Version: 1, Stage: "scanning", LastPath: lastPath, FilesSeen: at + 1, MediaImported: at + 1, TotalFiles: len(names)}
			var resumed []string
			result, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, gov, nil, req, checkpoint, scanStarted, func(checkpoint jobCheckpoint, _ store.JobProgress) error {
				resumed = append(resumed, checkpoint.LastPath)
				return nil
			})
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if !slices.Equal(resumed, full[at+1:]) {
				t.Fatalf("resumed walk visited %q, want the full walk's tail %q", resumed, full[at+1:])
			}
			if logs.Len() != 0 {
				t.Fatalf("resumed walk read a folder before its checkpoint: %s", logs.String())
			}
			if result.FilesSeen != len(names) || result.FilesMissing != 0 {
				t.Fatalf("result seen=%d missing=%d, want %d and 0", result.FilesSeen, result.FilesMissing, len(names))
			}
		})
	}
}

// TestScanResumePastPrunedFoldersStillMarksMissing proves the empty-root
// guard counts the folders a resumed walk skipped: a file deleted after the
// checkpoint is still marked missing when every remaining file sorts before
// the checkpoint, rather than the walk being taken for an empty mount.
func TestScanResumePastPrunedFoldersStillMarksMissing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/1.mkv", "a/2.mkv", "b/3.mkv", "c/4.mkv"} {
		writeScanFile(t, root, name, []byte("bytes of "+name))
	}
	st := storetest.Open(t)
	req := Request{LibraryID: "library-local", Root: root}
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, req); err != nil {
		t.Fatalf("seed scan: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	scanStarted := time.Now().UTC()
	var first []string
	_, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{Version: 1, Stage: "scanning"}, scanStarted, func(checkpoint jobCheckpoint, _ store.JobProgress) error {
		first = append(first, checkpoint.LastPath)
		if len(first) == 3 {
			return jobs.ErrYielded
		}
		return nil
	})
	if !errors.Is(err, jobs.ErrYielded) {
		t.Fatalf("first run error = %v, want yield", err)
	}
	for _, name := range []string{"b/3.mkv", "c/4.mkv"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	logs := captureScanWarnings(t)
	result, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, nil, nil, req, jobCheckpoint{Version: 1, Stage: "scanning", LastPath: first[2], FilesSeen: 3, MediaImported: 3, TotalFiles: 4}, scanStarted, nil)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if strings.Contains(logs.String(), "walked no media files") {
		t.Fatalf("resume past pruned folders was taken for an empty root: %s", logs.String())
	}
	if result.FilesMissing != 1 {
		t.Fatalf("files missing = %d, want 1 (c/4.mkv)", result.FilesMissing)
	}
}

// concurrencyProber records the peak number of probes in flight.
type concurrencyProber struct {
	active, peak, calls atomic.Int64
	probe               ffmpeg.Probe
}

func (p *concurrencyProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	p.calls.Add(1)
	n := p.active.Add(1)
	for peak := p.peak.Load(); n > peak && !p.peak.CompareAndSwap(peak, n); peak = p.peak.Load() {
	}
	time.Sleep(20 * time.Millisecond)
	p.active.Add(-1)
	return p.probe, nil
}

// TestContextualPreflightProbesWithinWorkerBudget proves a multi-file
// folder's preflight probes run in parallel up to the scan's worker budget,
// never beyond it, and decide the folder exactly as one at a time does
// (CORE-2353).
func TestContextualPreflightProbesWithinWorkerBudget(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			for i := range 12 {
				writeScanFile(t, root, fmt.Sprintf("group/%02d.mp4", i+1), []byte(fmt.Sprintf("file %d", i)))
			}
			st := storetest.Open(t)
			library, err := st.CreateLibrary(context.Background(), "Contextual", false, "contextual", "profile-default")
			if err != nil {
				t.Fatal(err)
			}
			prober := &concurrencyProber{probe: ffmpeg.Probe{HasVideo: true, DurationSeconds: 60, Tags: map[string]string{"group": "valid"}}}
			parsers := map[string]FilenameParser{"contextual": taggedContextualParser{}}
			result, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root})
			if err != nil {
				t.Fatal(err)
			}
			if result.MediaImported != 12 || result.FilesSeen != 12 {
				t.Fatalf("imported %d media from %d files, want 12 from 12", result.MediaImported, result.FilesSeen)
			}
			if peak := prober.peak.Load(); peak > int64(workers) {
				t.Fatalf("first scan: peak probes in flight = %d, over the budget of %d", peak, workers)
			}
			// A rescan of unchanged files takes the fast path, so the only
			// probes left are the folder preflight's.
			time.Sleep(2 * time.Millisecond)
			prober.peak.Store(0)
			prober.calls.Store(0)
			result, err = ScanWithWorkerGovernor(context.Background(), st, prober, nil, parsers, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: library.ID, Root: root})
			if err != nil {
				t.Fatal(err)
			}
			if result.FilesSeen != 12 || result.FilesAdded+result.FilesChanged != 0 {
				t.Fatalf("rescan seen=%d added=%d changed=%d, want 12 unchanged", result.FilesSeen, result.FilesAdded, result.FilesChanged)
			}
			if calls := prober.calls.Load(); calls != 12 {
				t.Fatalf("rescan probes = %d, want one preflight probe per file", calls)
			}
			peak := prober.peak.Load()
			if peak > int64(workers) {
				t.Fatalf("peak probes in flight = %d, over the budget of %d", peak, workers)
			}
			if workers > 1 && peak < 2 {
				t.Fatalf("peak probes in flight = %d, want the folder probed in parallel", peak)
			}
		})
	}
}
