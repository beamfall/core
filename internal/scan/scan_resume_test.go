package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type resumableImageDeriver struct {
	mu       sync.Mutex
	fresh    map[string]string
	work     int
	inFlight int
	peak     int
	overlap  chan struct{}
	once     sync.Once
}

type failingImageDeriver struct{ failPath string }

func (d failingImageDeriver) DeriveImage(_ context.Context, _ store.MediaID, path, _ string) error {
	if path == d.failPath {
		return errors.New("synthetic derivative failure")
	}
	return nil
}

type cancellableBlockingDeriver struct {
	started chan struct{}
	once    sync.Once
	active  atomic.Int64
}

func (d *cancellableBlockingDeriver) DeriveImage(ctx context.Context, _ store.MediaID, _, _ string) error {
	d.active.Add(1)
	defer d.active.Add(-1)
	d.once.Do(func() { close(d.started) })
	<-ctx.Done()
	return ctx.Err()
}

// staticCanceledDeriver deterministically returns context.Canceled without
// ever canceling the parent context, forcing completeFirst's select onto the
// completed.done branch (never the ctx.Done() branch) so the wrap path is
// exercised on every run instead of racing cancellableBlockingDeriver's two
// simultaneously-ready channels.
type staticCanceledDeriver struct{}

func (staticCanceledDeriver) DeriveImage(context.Context, store.MediaID, string, string) error {
	return context.Canceled
}

func newResumableImageDeriver() *resumableImageDeriver {
	return &resumableImageDeriver{fresh: map[string]string{}, overlap: make(chan struct{})}
}

// DeriveImage always releases inFlight (even on cancellation) and only
// counts work when a derivative genuinely commits — a canceled-but-admitted
// attempt that gets retried after restart must not be double-counted as
// extra work alongside the fresh derivation that actually completes it.
func (d *resumableImageDeriver) DeriveImage(ctx context.Context, _ store.MediaID, path, fingerprint string) error {
	d.mu.Lock()
	if d.fresh[path] == fingerprint {
		d.mu.Unlock()
		return nil
	}
	d.inFlight++
	if d.inFlight > d.peak {
		d.peak = d.inFlight
	}
	if d.inFlight >= 2 {
		d.once.Do(func() { close(d.overlap) })
	}
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.inFlight--
		d.mu.Unlock()
	}()

	select {
	case <-d.overlap:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	d.mu.Lock()
	d.fresh[path] = fingerprint
	d.work++
	d.mu.Unlock()
	return nil
}

func TestPhotoScanProgressConcurrencyAndRestartResume(t *testing.T) {
	// feature:photo-scan-pass
	const files = 201
	root := t.TempDir()
	for i := 0; i < files; i++ {
		writeScanFile(t, root, "photo-"+zeroPad(i)+".jpg", []byte("photo "+zeroPad(i)))
	}
	st := storetest.Open(t)
	created := time.Unix(200, 0).UTC()
	job, err := NewJob(JobRequest{ID: "job-photo-pass", LibraryID: "library-local", Root: root, CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	deriver := newResumableImageDeriver()
	for run, wantCurrent := range []int64{100, 200, 201} {
		registry := jobs.NewRegistry(st, "photo-worker-"+zeroPad(run))
		registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2), deriver))
		if _, err := registry.RunOne(context.Background(), nil, created.Add(time.Duration(run)*time.Second)); err != nil {
			t.Fatalf("run %d: %v", run+1, err)
		}
		got, err := st.Job(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Progress.Current != wantCurrent || got.Progress.Total != files || got.Progress.Unit != "files" {
			t.Fatalf("run %d progress = %#v, want %d/%d files", run+1, got.Progress, wantCurrent, files)
		}
		checkpoint := mustDecodeCheckpoint(t, got.Checkpoint)
		if checkpoint.FilesSeen != int(wantCurrent) || checkpoint.TotalFiles != files {
			t.Fatalf("run %d checkpoint = %#v", run+1, checkpoint)
		}
	}
	completed, err := st.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("status = %q, want completed", completed.Status)
	}
	deriver.mu.Lock()
	defer deriver.mu.Unlock()
	if deriver.work != files {
		t.Fatalf("derivative work = %d, want exactly %d across recreated registries", deriver.work, files)
	}
	if deriver.peak < 2 || deriver.peak > 2 {
		t.Fatalf("peak derivative work = %d, want governed concurrency 2", deriver.peak)
	}
}

func TestPhotoScanCheckpointStopsBeforeFailedDerivative(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 60; i++ {
		writeScanFile(t, root, "photo-"+zeroPad(i)+".jpg", []byte("photo "+zeroPad(i)))
	}
	st := storetest.Open(t)
	created := time.Unix(300, 0).UTC()
	job, err := NewJob(JobRequest{ID: "job-photo-failure", LibraryID: "library-local", Root: root, CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	failPath := filepath.Join(root, "photo-050.jpg")
	registry := jobs.NewRegistry(st, "photo-failure-worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2), failingImageDeriver{failPath: failPath}))
	_, _ = registry.RunOne(context.Background(), nil, created)
	failed, err := st.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := mustDecodeCheckpoint(t, failed.Checkpoint)
	if checkpoint.FilesSeen != 50 || checkpoint.LastPath != filepath.Join(root, "photo-049.jpg") {
		t.Fatalf("failed derivative advanced checkpoint: %#v", checkpoint)
	}
	if failed.Progress.Current != 50 || failed.Progress.Total != 60 {
		t.Fatalf("failed derivative progress = %#v, want 50/60", failed.Progress)
	}
}

func TestPhotoScanCancellationDrainsBlockingDeriver(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "photo-000.jpg", []byte("photo 0"))
	writeScanFile(t, root, "photo-001.jpg", []byte("photo 1"))
	st := storetest.Open(t)
	deriver := &cancellableBlockingDeriver{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ScanWithWorkerGovernor(ctx, st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2), Request{LibraryID: "library-local", Root: root}, deriver)
		done <- err
	}()
	<-deriver.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("scan error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not drain canceled derivative goroutines")
	}
	if active := deriver.active.Load(); active != 0 {
		t.Fatalf("active derivative goroutines = %d, want 0", active)
	}
}

// TestPhotoScanDerivativeCancellationPreservesIdentity is deterministic (no
// racing channels): the deriver returns context.Canceled under a
// non-canceled parent, so completeFirst's wrap must preserve both
// errImageDerivation and context.Canceled identities for errors.Is.
func TestPhotoScanDerivativeCancellationPreservesIdentity(t *testing.T) {
	root := t.TempDir()
	writeScanFile(t, root, "photo-000.jpg", []byte("photo 0"))
	st := storetest.Open(t)
	_, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(1), Request{LibraryID: "library-local", Root: root}, staticCanceledDeriver{})
	if !errors.Is(err, errImageDerivation) {
		t.Fatalf("scan error = %v, want errImageDerivation", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("scan error = %v, want context.Canceled", err)
	}
}

// TestCompareWalkOrderMatchesWalkDir pins compareWalkOrder to the order
// filepath.WalkDir really visits a tree whose sibling names sort around the
// separator ('-' and '.' below '/'), where a byte compare of full paths
// disagrees (CORE-0737).
func TestCompareWalkOrderMatchesWalkDir(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a/b/one.mp4", "a/b/two.mp4", "a/z.mp4", "a-c/x.mp4", "a.b/y.mp4", "a0.mp4", "b.mp4"} {
		writeScanFile(t, root, name, []byte(name))
	}
	var walked []string
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			walked = append(walked, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sorted := slices.Clone(walked)
	slices.SortFunc(sorted, func(a, b string) int { return compareWalkOrder(root, a, b) })
	if !slices.Equal(sorted, walked) {
		t.Fatalf("compareWalkOrder order = %q, WalkDir order = %q", sorted, walked)
	}
	bytewise := slices.Clone(walked)
	slices.Sort(bytewise)
	if slices.Equal(bytewise, walked) {
		t.Fatal("fixture no longer separates byte order from WalkDir order")
	}
	if compareWalkOrder(root, filepath.Join(root, "a"), filepath.Join(root, "a", "z.mp4")) >= 0 {
		t.Fatal("a directory must sort before its own entries")
	}
}

// TestScanResumeInsideNestedDirWalksSiblingsOnce is the CORE-0737 resume
// acceptance: a scan that yields inside a/b/ and resumes from that checkpoint
// (in the JSON shape the previous build wrote) walks each unvisited file,
// including the a-c/ sibling that sorts below a/ bytewise, exactly once, and
// marks none of them missing.
func TestScanResumeInsideNestedDirWalksSiblingsOnce(t *testing.T) {
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers=%d", workers), func(t *testing.T) {
			root := t.TempDir()
			names := []string{"a/b/one.mp4", "a/b/two.mp4", "a/b/three.mp4", "a/z.mp4", "a-c/x.mp4", "a-c/y.mp4", "b.mp4"}
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

			// First run: yield after the three files under a/b/.
			var firstRun []string
			_, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, gov, nil, req, jobCheckpoint{Version: 1, Stage: "scanning"}, scanStarted, func(checkpoint jobCheckpoint, _ store.JobProgress) error {
				firstRun = append(firstRun, checkpoint.LastPath)
				if len(firstRun) == 3 {
					return jobs.ErrYielded
				}
				return nil
			})
			if !errors.Is(err, jobs.ErrYielded) {
				t.Fatalf("first run error = %v, want yield", err)
			}
			lastPath := filepath.Join(root, "a", "b", "two.mp4")
			if firstRun[len(firstRun)-1] != lastPath {
				t.Fatalf("first run visited %q, want to stop at %q", firstRun, lastPath)
			}

			// A checkpoint as the previous build persisted it.
			quoted, err := json.Marshal(lastPath)
			if err != nil {
				t.Fatal(err)
			}
			legacy := []byte(`{"version":1,"stage":"scanning","last_path":` + string(quoted) + `,"files_seen":3,"media_imported":3}`)
			checkpoint, err := checkpointFromJob(legacy)
			if err != nil {
				t.Fatal(err)
			}
			var resumed []string
			if _, err := scanFromCheckpoint(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, gov, nil, req, checkpoint, scanStarted, func(checkpoint jobCheckpoint, _ store.JobProgress) error {
				resumed = append(resumed, checkpoint.LastPath)
				return nil
			}); err != nil {
				t.Fatalf("resume: %v", err)
			}
			want := []string{
				filepath.Join(root, "a", "z.mp4"),
				filepath.Join(root, "a-c", "x.mp4"),
				filepath.Join(root, "a-c", "y.mp4"),
				filepath.Join(root, "b.mp4"),
			}
			if !slices.Equal(resumed, want) {
				t.Fatalf("resumed walk visited %q, want each unvisited file once: %q", resumed, want)
			}
			files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(names) {
				t.Fatalf("live files after resume = %d, want %d (none marked missing): %#v", len(files), len(names), files)
			}
		})
	}
}

func captureScanWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func seedScannedLibrary(t *testing.T, files int) (string, *store.Store, []string) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, files)
	for i := 0; i < files; i++ {
		paths = append(paths, writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i))))
	}
	st := storetest.Open(t)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("seed scan: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	return root, st, paths
}

func liveScanPaths(t *testing.T, st *store.Store) []string {
	t.Helper()
	files, err := st.MediaFilesForLibrary(context.Background(), "library-local")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	slices.Sort(paths)
	return paths
}

// TestScanUnavailableRootMarksNothingMissing is the CORE-0737 root
// acceptance: a removed, non-directory or unreadable root fails the scan with
// the reason, and an empty root completes with a warning; none of them marks
// the library's present files missing.
func TestScanUnavailableRootMarksNothingMissing(t *testing.T) {
	cases := []struct {
		name      string
		breakRoot func(t *testing.T, root string)
		wantErr   string
		wantLog   string
	}{
		{name: "removed", breakRoot: func(t *testing.T, root string) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "is unavailable"},
		{name: "not a directory", breakRoot: func(t *testing.T, root string) {
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(root, []byte("not a dir"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "is not a directory"},
		{name: "permission denied", breakRoot: func(t *testing.T, root string) {
			if os.Geteuid() == 0 {
				t.Skip("root ignores directory permissions")
			}
			if err := os.Chmod(root, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(root, 0o700) }) //nolint:gosec // G302: restores owner access so t.TempDir can be removed.
		}, wantErr: "cannot be read"},
		{name: "empty", breakRoot: func(t *testing.T, root string) {
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
					t.Fatal(err)
				}
			}
		}, wantLog: "walked no media files"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, st, paths := seedScannedLibrary(t, 3)
			tc.breakRoot(t, root)
			logs := captureScanWarnings(t)
			_, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("scan error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("scan error = %v, want success", err)
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Fatalf("warnings = %q, want %q", logs.String(), tc.wantLog)
			}
			if live := liveScanPaths(t, st); !slices.Equal(live, paths) {
				t.Fatalf("live files = %q, want all %q still present", live, paths)
			}
		})
	}
}

// TestScanDeletedFilesStillMarkedMissing proves the guards leave ordinary
// deletions alone: a handful removed from a populated root, or most of a
// library too small for the missing-ratio guard, are marked missing exactly.
func TestScanDeletedFilesStillMarkedMissing(t *testing.T) {
	cases := []struct {
		name    string
		files   int
		deleted []int
	}{
		{name: "handful from populated root", files: 30, deleted: []int{2, 11, 27}},
		{name: "most of a small library", files: 4, deleted: []int{0, 1, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, st, paths := seedScannedLibrary(t, tc.files)
			want := slices.Clone(paths)
			for _, i := range tc.deleted {
				if err := os.Remove(paths[i]); err != nil {
					t.Fatal(err)
				}
				want = slices.DeleteFunc(want, func(p string) bool { return p == paths[i] })
			}
			if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
				t.Fatalf("rescan: %v", err)
			}
			if live := liveScanPaths(t, st); !slices.Equal(live, want) {
				t.Fatalf("live files = %q, want exactly %q", live, want)
			}
		})
	}
}

// TestScanMissingRatioGuardSkipsMassMissing proves the named guard: a scan
// that would flip more than missingRatioGuardThreshold of a populated root's
// present files missing marks nothing and says why.
func TestScanMissingRatioGuardSkipsMassMissing(t *testing.T) {
	root, st, paths := seedScannedLibrary(t, missingRatioGuardMinPresent+10)
	removed := int(float64(len(paths))*missingRatioGuardThreshold) + 1
	for _, path := range paths[:removed] {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	logs := captureScanWarnings(t)
	if _, err := Scan(context.Background(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if !strings.Contains(logs.String(), "missing-ratio guard") {
		t.Fatalf("warnings = %q, want the missing-ratio guard reason", logs.String())
	}
	if live := liveScanPaths(t, st); !slices.Equal(live, paths) {
		t.Fatalf("live files = %q, want all %q still present", live, paths)
	}
}
