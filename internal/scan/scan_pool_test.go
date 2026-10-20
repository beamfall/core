package scan

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/beamfall/core/internal/ffmpeg"
	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fixedGovernor is a WorkerGovernor pinned to a worker count — the test's
// stand-in for governor.Governor's ADR-0013 §2 worker-pool sizing.
type fixedGovernor int

func (g fixedGovernor) GenerationWorkers() int { return int(g) }

// atomicProber counts probes race-safely (pool workers probe concurrently)
// and returns one fixed probe for every file.
type atomicProber struct {
	probe ffmpeg.Probe
	calls atomic.Int64
}

type combinedWorkTracker struct {
	active       atomic.Int64
	peak         atomic.Int64
	hashActive   atomic.Int64
	deriveActive atomic.Int64
	hashRuns     atomic.Int64
	deriveRuns   atomic.Int64
}

func (t *combinedWorkTracker) run(kind string) {
	var own *atomic.Int64
	if kind == "hash" {
		own = &t.hashActive
		t.hashRuns.Add(1)
	} else {
		own = &t.deriveActive
		t.deriveRuns.Add(1)
	}
	own.Add(1)
	n := t.active.Add(1)
	for peak := t.peak.Load(); n > peak && !t.peak.CompareAndSwap(peak, n); peak = t.peak.Load() {
	}
	time.Sleep(10 * time.Millisecond)
	t.active.Add(-1)
	own.Add(-1)
}

type trackedImageDeriver struct{ tracker *combinedWorkTracker }

func (d trackedImageDeriver) DeriveImage(context.Context, store.MediaID, string, string) error {
	d.tracker.run("derive")
	return nil
}

func (p *atomicProber) Probe(context.Context, string) (ffmpeg.Probe, error) {
	p.calls.Add(1)
	return p.probe, nil
}

// countHashesAtomic mirrors countHashes with a race-safe counter: pool
// workers call the hash seam concurrently, so the sequential test helper's
// plain int would be a data race under -race.
func countHashesAtomic(t *testing.T) *atomic.Int64 {
	t.Helper()
	calls := &atomic.Int64{}
	original := hashFileContent
	hashFileContent = func(path string) (string, error) {
		calls.Add(1)
		return original(path)
	}
	t.Cleanup(func() { hashFileContent = original })
	return calls
}

// poolFixtureTree writes a small representative library: nested video files,
// duplicate content across paths, an image (ADR-0043 class-by-extension), an
// audio container, and an Extras-convention file.
func poolFixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeScanFile(t, root, "Alpha.mp4", []byte("alpha video bytes"))
	writeScanFile(t, root, "nested/Beta.mkv", []byte("beta video bytes"))
	writeScanFile(t, root, "nested/deeper/Gamma.mp4", []byte("gamma video bytes"))
	writeScanFile(t, root, "moved/Alpha Copy.mp4", []byte("alpha video bytes"))
	writeScanFile(t, root, "Stills/photo.jpg", []byte("jpeg bytes"))
	writeScanFile(t, root, "Music/track.mp3", []byte("audio bytes"))
	writeScanFile(t, root, "Alpha-trailer.mp4", []byte("trailer bytes"))
	return root
}

func TestScanWorkerPoolMatchesSequentialResults(t *testing.T) {
	// feature:scan-pipeline
	// REVSWEEP-PERF-2 acceptance: on the same fixture tree, the
	// governor-bounded pool must produce results identical to the sequential
	// walk — same counts, same walk-ordered Files, same fingerprints, same
	// imported titles.
	root := poolFixtureTree(t)
	probe := ffmpeg.Probe{Container: "mp4", VideoCodec: "h264", HasVideo: true, DurationSeconds: 60}

	sequentialStore := storetest.Open(t)
	sequential, err := Scan(context.Background(), sequentialStore, &atomicProber{probe: probe}, nil, nil, nil, nil, Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("sequential scan: %v", err)
	}

	pooledStore := storetest.Open(t)
	pooled, err := ScanWithWorkerGovernor(context.Background(), pooledStore, &atomicProber{probe: probe}, nil, nil, nil, nil, nil, nil, fixedGovernor(4), Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("pooled scan: %v", err)
	}

	if pooled.FilesSeen != sequential.FilesSeen {
		t.Fatalf("pooled FilesSeen = %d, sequential = %d", pooled.FilesSeen, sequential.FilesSeen)
	}
	if pooled.MediaImported != sequential.MediaImported {
		t.Fatalf("pooled MediaImported = %d, sequential = %d", pooled.MediaImported, sequential.MediaImported)
	}
	if pooled.LastPath != sequential.LastPath {
		t.Fatalf("pooled LastPath = %q, sequential = %q", pooled.LastPath, sequential.LastPath)
	}
	if len(pooled.Files) != len(sequential.Files) {
		t.Fatalf("pooled imported %d files, sequential %d", len(pooled.Files), len(sequential.Files))
	}
	for i := range sequential.Files {
		if pooled.Files[i].Path != sequential.Files[i].Path {
			t.Fatalf("file %d path = %q, sequential = %q (walk order must be preserved)", i, pooled.Files[i].Path, sequential.Files[i].Path)
		}
		if pooled.Files[i].ContentFingerprint != sequential.Files[i].ContentFingerprint {
			t.Fatalf("file %d (%s) fingerprint diverged under the pool", i, pooled.Files[i].Path)
		}
	}
	for _, st := range []*store.Store{sequentialStore, pooledStore} {
		items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		// "Alpha" and "moved/Alpha Copy" share content, so they dedupe into one
		// Media whose title follows the later-walked path — in both stores.
		for _, title := range []string{"Alpha Copy", "Beta", "Gamma", "track"} {
			if !hasSourceItemWithTitle(items, title) {
				t.Fatalf("store missing %q import: %#v", title, items)
			}
		}
	}
}

func TestScanWorkerPoolConcurrencyBoundedByGovernor(t *testing.T) {
	// feature:scan-pipeline
	// REVSWEEP-PERF-2 acceptance: in-flight expensive work is bounded by the
	// governor's worker budget — never more than GenerationWorkers hashes in
	// flight — and the pool genuinely reaches that bound (two hashes overlap).
	const workers = 2
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}

	var inFlight, peak atomic.Int64
	overlap := make(chan struct{})
	var overlapOnce sync.Once
	original := hashFileContent
	hashFileContent = func(path string) (string, error) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := peak.Load()
			if n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		if n >= workers {
			overlapOnce.Do(func() { close(overlap) })
		}
		// Hold the slot briefly so a second worker can arrive; release either
		// when overlap is proven or after the grace period (so a regression to
		// sequential scanning fails the overlap assertion instead of hanging).
		select {
		case <-overlap:
		case <-time.After(2 * time.Second):
		}
		return original(path)
	}
	t.Cleanup(func() { hashFileContent = original })

	st := storetest.Open(t)
	result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(workers), Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("pooled scan: %v", err)
	}
	if result.FilesSeen != 8 {
		t.Fatalf("FilesSeen = %d, want 8", result.FilesSeen)
	}
	if got := peak.Load(); got > workers {
		t.Fatalf("peak concurrent hashes = %d, want <= governor budget %d", got, workers)
	}
	select {
	case <-overlap:
	default:
		t.Fatalf("hashing never reached %d concurrent workers; the pool did not parallelize", workers)
	}
}

func TestScanWorkerPoolFastPathStillSkipsRehash(t *testing.T) {
	// feature:scan-pipeline
	// The pool moves the fast-path triple lookup into the workers; an
	// unchanged library must still perform ZERO hashes and ZERO probes
	// (ADR-0009 identity preserved) on a pooled re-scan.
	root := t.TempDir()
	writeScanFile(t, root, "a.mp4", []byte("alpha content"))
	writeScanFile(t, root, "nested/b.mp4", []byte("beta content"))
	st := storetest.Open(t)
	prober := &atomicProber{probe: ffmpeg.Probe{Container: "mp4", VideoCodec: "h264", HasVideo: true, DurationSeconds: 60}}
	hashes := countHashesAtomic(t)
	gov := fixedGovernor(3)

	first, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, nil, nil, nil, nil, nil, gov, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("first pooled scan: %v", err)
	}
	if first.FilesSeen != 2 || hashes.Load() != 2 || prober.calls.Load() != 2 {
		t.Fatalf("first pooled scan: files=%d hashes=%d probes=%d, want 2/2/2", first.FilesSeen, hashes.Load(), prober.calls.Load())
	}

	hashes.Store(0)
	prober.calls.Store(0)
	second, err := ScanWithWorkerGovernor(context.Background(), st, prober, nil, nil, nil, nil, nil, nil, gov, Request{LibraryID: "library-local", Root: root})
	if err != nil {
		t.Fatalf("second pooled scan: %v", err)
	}
	if second.FilesSeen != 2 {
		t.Fatalf("second pooled scan FilesSeen = %d, want 2", second.FilesSeen)
	}
	if hashes.Load() != 0 || prober.calls.Load() != 0 {
		t.Fatalf("pooled fast-path performed %d hashes / %d probes, want 0/0", hashes.Load(), prober.calls.Load())
	}
}

func TestScanWorkerPoolSurvivesUnreadableFile(t *testing.T) {
	// feature:scan-pipeline
	// The sequential walk's resilience contract holds under the pool: one
	// permission-denied file (its hash fails in a worker) is logged and
	// skipped; the rest of the tree still imports. See
	// TestScanSurvivesUnreadableFileMidWalk for the root/permissions caveat.
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode-000 files stay readable, so the permission-denied path is unreachable")
	}
	root := t.TempDir()
	denied := writeScanFile(t, root, "denied.mp4", []byte("secret video bytes"))
	writeScanFile(t, root, "readable.mp4", []byte("video bytes"))
	if err := os.Chmod(denied, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o600) })

	st := storetest.Open(t)
	result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(4), Request{
		LibraryID: "library-local",
		Root:      root,
	})
	if err != nil {
		t.Fatalf("pooled scan returned error (an unreadable file must not abort the walk): %v", err)
	}
	if result.MediaImported != 1 {
		t.Fatalf("MediaImported = %d, want 1 (the readable movie; denied.mp4 skipped)", result.MediaImported)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "readable") || hasSourceItemWithTitle(items, "denied") {
		t.Fatalf("pooled scan import set wrong after unreadable file: %#v", items)
	}
}

func TestScanJobWorkerPoolYieldsCheckpointAndResumes(t *testing.T) {
	// feature:scan-pipeline
	// Crash-resume semantics are unchanged under the pool (REVSWEEP-PERF-2
	// acceptance): the batch yield still lands on the walk-ordered LastPath —
	// never a later file a worker had already fingerprinted — and the resumed
	// run imports exactly the remainder.
	root := t.TempDir()
	for i := 0; i < 101; i++ {
		writeScanFile(t, root, "clip-"+zeroPad(i)+".mp4", []byte("clip "+zeroPad(i)))
	}
	st := storetest.Open(t)
	now := time.Unix(100, 0).UTC()
	job, err := NewJob(JobRequest{
		ID:        "job-scan-pool-yield",
		LibraryID: "library-local",
		Root:      root,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("NewJob returned error: %v", err)
	}
	if _, err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(4)))

	if _, err := registry.RunOne(context.Background(), nil, now); err != nil {
		t.Fatalf("first RunOne returned error: %v", err)
	}
	yielded, err := st.Job(context.Background(), "job-scan-pool-yield")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("first status = %q error=%q, want yielded", yielded.Status, yielded.Error)
	}
	yieldedCheckpoint := mustDecodeCheckpoint(t, yielded.Checkpoint)
	expectedYieldPath := filepath.Clean(filepath.Join(root, "clip-099.mp4"))
	if yieldedCheckpoint.Stage != "scanning" || yieldedCheckpoint.FilesSeen != 100 || yieldedCheckpoint.LastPath != expectedYieldPath {
		t.Fatalf("yielded checkpoint = %#v, want scanning at %q", yieldedCheckpoint, expectedYieldPath)
	}

	if _, err := registry.RunOne(context.Background(), nil, now.Add(time.Second)); err != nil {
		t.Fatalf("second RunOne returned error: %v", err)
	}
	completed, err := st.Job(context.Background(), "job-scan-pool-yield")
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != store.JobStatusCompleted {
		t.Fatalf("second status = %q error=%q, want completed", completed.Status, completed.Error)
	}
	completedCheckpoint := mustDecodeCheckpoint(t, completed.Checkpoint)
	if completedCheckpoint.Stage != "complete" || completedCheckpoint.FilesSeen != 101 {
		t.Fatalf("completed checkpoint = %#v, want complete with 101 files", completedCheckpoint)
	}
	items, err := st.SourceItems(context.Background(), store.LibraryFilter{AllowedIDs: []store.LibraryID{"library-local"}}, store.SourceQuery{Kind: "recently-added", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !hasSourceItemWithTitle(items, "clip-000") || !hasSourceItemWithTitle(items, "clip-100") {
		t.Fatalf("sourceItems missing first/last yielded imports: %#v", items)
	}
}

func TestPhotoScanSharesGovernorBudgetAcrossFillAndDerivation(t *testing.T) {
	const workers = 2
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		writeScanFile(t, root, "photo-"+zeroPad(i)+".jpg", []byte("photo "+zeroPad(i)))
	}
	tracker := &combinedWorkTracker{}
	originalHash := hashFileContent
	hashFileContent = func(path string) (string, error) {
		tracker.run("hash")
		return originalHash(path)
	}
	t.Cleanup(func() { hashFileContent = originalHash })
	st := storetest.Open(t)
	result, err := ScanWithWorkerGovernor(context.Background(), st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(workers), Request{LibraryID: "library-local", Root: root}, trackedImageDeriver{tracker: tracker})
	if err != nil {
		t.Fatal(err)
	}
	if result.FilesSeen != 40 {
		t.Fatalf("files seen = %d, want 40", result.FilesSeen)
	}
	if peak := tracker.peak.Load(); peak < 2 || peak > workers {
		t.Fatalf("combined fill+derive peak = %d, want 2 and never above governor budget", peak)
	}
	if tracker.hashRuns.Load() == 0 || tracker.deriveRuns.Load() == 0 {
		t.Fatalf("combined-budget stages did not both run: hashes=%d derivatives=%d", tracker.hashRuns.Load(), tracker.deriveRuns.Load())
	}
}
