package scan

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func enqueueHashTestScan(t *testing.T, st *store.Store, id store.JobID, root string, now time.Time) {
	t.Helper()
	job, err := NewJob(JobRequest{ID: id, LibraryID: "library-local", Root: root, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
}

func TestScanJobsSerializeWholeFileHashes(t *testing.T) {
	// feature:scan-pipeline
	now := time.Now().UTC()
	st := storetest.Open(t)
	firstRoot, secondRoot := t.TempDir(), t.TempDir()
	firstPath := writeScanFile(t, firstRoot, "first.mp4", []byte("first"))
	secondPath := writeScanFile(t, secondRoot, "second.mp4", []byte("second"))
	enqueueHashTestScan(t, st, "first", firstRoot, now)
	enqueueHashTestScan(t, st, "second", secondRoot, now.Add(time.Millisecond))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	var runs sync.WaitGroup
	defer func() {
		cancel()
		runs.Wait()
	}()
	firstHash := make(chan struct{})
	firstHashReturned := make(chan struct{})
	secondHash := make(chan struct{})
	secondWaiting := make(chan struct{})
	releaseFirst := make(chan struct{})
	originalHash, originalWait := hashFileContent, onScanHashWait
	onScanHashWait = func(path string) {
		if path == secondPath {
			close(secondWaiting)
		}
	}
	hashFileContent = func(hashCtx context.Context, path string) (string, error) {
		switch path {
		case firstPath:
			defer close(firstHashReturned)
			close(firstHash)
			select {
			case <-releaseFirst:
			case <-ctx.Done(): // Bound teardown even if the scan drops cancellation.
				return "", ctx.Err()
			}
		case secondPath:
			select {
			case <-firstHashReturned:
			default:
				t.Error("second job entered its hash before the first hash returned")
			}
			close(secondHash)
		}
		return originalHash(hashCtx, path)
	}
	t.Cleanup(func() { hashFileContent, onScanHashWait = originalHash, originalWait })

	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2)))
	run := func() <-chan error {
		done := make(chan error, 1)
		runs.Go(func() {
			_, err := registry.RunOne(ctx, nil, now.Add(time.Second))
			done <- err
		})
		return done
	}
	firstDone := run()
	select {
	case <-firstHash:
	case <-ctx.Done():
		t.Fatal("first job never started hashing")
	}
	secondDone := run()
	select {
	case <-secondWaiting:
	case <-secondHash:
		t.Fatal("second job entered its hash while the first job was hashing")
	case <-ctx.Done():
		t.Fatal("second job never waited for the occupied hash slot")
	}
	close(releaseFirst)
	for _, done := range []<-chan error{firstDone, secondDone} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []store.JobID{"first", "second"} {
		job, err := st.Job(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != store.JobStatusCompleted {
			t.Fatalf("job %s status = %s (%s), want completed", id, job.Status, job.Error)
		}
	}
	select {
	case <-secondHash:
	default:
		t.Fatal("second job never started hashing after the slot was released")
	}
}

func TestScanJobYieldClosesInFlightHashReader(t *testing.T) {
	// feature:scan-pipeline
	now := time.Now().UTC()
	st := storetest.Open(t)
	root := t.TempDir()
	first := writeScanFile(t, root, "a.mp4", []byte("first"))
	writeScanFile(t, root, "b.mp4", []byte("second"))
	// Fill the two-worker completion buffer from cache so the first file can
	// checkpoint while the next file still hashes.
	if _, err := Scan(t.Context(), st, nil, nil, nil, nil, nil, Request{LibraryID: "library-local", Root: root}); err != nil {
		t.Fatal(err)
	}
	pending := writeScanFile(t, root, "c.mp4", []byte("still more data"))
	runCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reading, closed := make(chan struct{}), make(chan struct{})
	original := hashFileContent
	hashFileContent = func(ctx context.Context, path string) (string, error) {
		if path != pending {
			return original(ctx, path)
		}
		file, err := os.OpenInRoot(root, "c.mp4")
		if err != nil {
			return "", err
		}
		defer func() {
			_ = file.Close()
			close(closed)
		}()
		var chunk [1]byte
		if _, err := io.ReadFull(file, chunk[:]); err != nil {
			return "", err
		}
		close(reading)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-runCtx.Done():
			return "", runCtx.Err()
		}
	}
	savedBudget, savedClock := scanJobRunBudget, scanJobClock
	t.Cleanup(func() {
		hashFileContent = original
		scanJobRunBudget, scanJobClock = savedBudget, savedClock
	})
	scanJobRunBudget = 0
	scanJobClock = func() time.Time {
		select {
		case <-reading:
		case <-runCtx.Done():
		}
		return now
	}
	enqueueHashTestScan(t, st, "yield-mid-hash", root, now)
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, HandlerWithWorkerGovernor(st, nil, nil, nil, nil, nil, nil, nil, fixedGovernor(2)))
	if _, err := registry.RunOne(runCtx, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := runCtx.Err(); err != nil {
		t.Fatalf("scan did not yield before the outer context ended: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("yield returned with the hash reader still open")
	}
	yielded, err := st.Job(t.Context(), "yield-mid-hash")
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded {
		t.Fatalf("status = %s (%s), want yielded", yielded.Status, yielded.Error)
	}
	checkpoint := mustDecodeCheckpoint(t, yielded.Checkpoint)
	if checkpoint.LastPath != first || checkpoint.FilesSeen != 1 {
		t.Fatalf("checkpoint = %+v, want one completed file at %q", checkpoint, first)
	}
}
