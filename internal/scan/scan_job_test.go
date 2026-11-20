package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestScanJobFailsFastWhenRootIsMissing: a scan whose Library folder is gone
// fails on its first claim, every time, and records which folder and why, so
// it neither walks nor comes back as a yielded job (CORE-2309).
func TestScanJobFailsFastWhenRootIsMissing(t *testing.T) {
	// feature:scan-pipeline
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "gone")
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))

	for _, id := range []store.JobID{"job-missing-1", "job-missing-2"} {
		job, err := NewJob(JobRequest{ID: id, LibraryID: library.ID, Root: root, CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.EnqueueJob(ctx, job); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
			t.Fatalf("RunOne %s: %v", id, err)
		}
		got, err := st.Job(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != store.JobStatusFailed {
			t.Fatalf("%s status = %q, want failed on the first claim", id, got.Status)
		}
		events, err := st.ListJobEvents(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var found *ScanRootUnavailable
		for _, event := range events {
			if event.Type == ScanRootUnavailableEvent {
				var payload ScanRootUnavailable
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				found = &payload
			}
		}
		want := ScanRootUnavailable{LibraryID: string(library.ID), Root: root, Reason: "missing"}
		if found == nil || *found != want {
			t.Fatalf("%s root event = %+v, want %+v", id, found, want)
		}
	}
}

func TestBatchScanSkipsLibraryTrashedAfterEnqueue(t *testing.T) {
	ctx := t.Context()
	st := storetest.Open(t)
	var targets []JobRoot
	for _, title := range []string{"Trashed", "Live"} {
		root := t.TempDir()
		if title == "Trashed" {
			root = filepath.Join(root, "gone")
		}
		library, err := st.CreateLibraryWithRoots(ctx, title, false, "", "profile-default", []string{root}, "")
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, JobRoot{LibraryID: library.ID, Root: root})
	}
	job, err := NewBatchJob(targets, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	job, err = st.EnqueueJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLibrary(ctx, targets[0].LibraryID); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))
	for range targets {
		if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	_ = completedScanSummary(t, st, job.ID)
	events, err := st.ListJobEvents(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == ScanRootUnavailableEvent {
			t.Fatal("worker checked the filesystem for a trashed Library")
		}
	}
}

// TestScanJobYieldsWhenRunBudgetIsSpent: a batch of slow files yields once the
// run has used its time budget, not only after a fixed file count, so a slow
// scan cannot hold a worker for the length of a whole batch (CORE-2306).
func TestScanJobYieldsWhenRunBudgetIsSpent(t *testing.T) {
	// feature:scan-pipeline
	ctx := context.Background()
	root := t.TempDir()
	for _, name := range []string{"clip-a.mp4", "clip-b.mp4", "clip-c.mp4"} {
		writeScanFile(t, root, name, []byte(name))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	saved := scanJobRunBudget
	scanJobRunBudget = 0
	t.Cleanup(func() { scanJobRunBudget = saved })

	job, err := NewJob(JobRequest{ID: "job-budget", LibraryID: library.ID, Root: root, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))
	if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	yielded, err := st.Job(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if yielded.Status != store.JobStatusYielded || yielded.Progress.Current >= 3 {
		t.Fatalf("after a spent budget job = %s at %d files, want yielded before all 3", yielded.Status, yielded.Progress.Current)
	}

	scanJobRunBudget = saved
	for run := 0; run < 3; run++ {
		current, err := st.Job(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status == store.JobStatusCompleted {
			break
		}
		if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if summary := completedScanSummary(t, st, job.ID); summary.FilesChecked != 3 || summary.Added != 3 {
		t.Fatalf("resumed scan summary = %+v, want all 3 files added", summary)
	}
}

// TestScanJobRunBudgetStartsAtFirstCheckpoint: the run budget counts from the
// run's first checkpoint, so the walk a resumed run makes back to its resume
// point does not spend it. When that walk outlasted the budget, every run
// yielded at its first checkpoint and a scan crept forward a few files per
// attempt (CORE-2353). The clock steps a minute per reading against a
// two-minute budget, so each run checkpoints exactly three files; reading it
// before the first checkpoint would cut that to two.
func TestScanJobRunBudgetStartsAtFirstCheckpoint(t *testing.T) {
	// feature:scan-pipeline
	ctx := context.Background()
	root := t.TempDir()
	const files = 7
	for i := 0; i < files; i++ {
		writeScanFile(t, root, fmt.Sprintf("clip-%d.mp4", i), []byte(fmt.Sprintf("clip-%d", i)))
	}
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Clips", false, "filmtv", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	savedBudget, savedClock := scanJobRunBudget, scanJobClock
	t.Cleanup(func() { scanJobRunBudget, scanJobClock = savedBudget, savedClock })
	scanJobRunBudget = 2 * time.Minute
	tick := time.Date(2026, 11, 17, 19, 46, 43, 0, time.UTC)
	scanJobClock = func() time.Time {
		tick = tick.Add(time.Minute)
		return tick
	}

	job, err := NewJob(JobRequest{ID: "job-budget-resume", LibraryID: library.ID, Root: root, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	registry := jobs.NewRegistry(st, "worker")
	registry.Register(JobKind, Handler(st, nil, nil, nil, nil, nil))
	for _, want := range []struct {
		status  store.JobStatus
		current int64
	}{
		{store.JobStatusYielded, 3},
		{store.JobStatusYielded, 6},
		{store.JobStatusCompleted, files},
	} {
		if _, err := registry.RunOne(ctx, nil, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		got, err := st.Job(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want.status || got.Progress.Current != want.current {
			t.Fatalf("after a run job = %s at %d files, want %s at %d", got.Status, got.Progress.Current, want.status, want.current)
		}
	}
	if summary := completedScanSummary(t, st, job.ID); summary.FilesChecked != files || summary.Added != files {
		t.Fatalf("resumed scan summary = %+v, want all %d files added", summary, files)
	}
}
