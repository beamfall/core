package scan

import (
	"context"
	"errors"
	"path/filepath"
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
