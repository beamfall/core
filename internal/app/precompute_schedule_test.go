package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/beamfall/core/internal/playback"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// TestLibraryPrecomputeScheduleShipsDisabledAndRunsOnceEnabled pins CORE-2326:
// every Library gets a precompute Schedule seeded disabled, Reconcile keeps it
// disabled, and once the operator enables it a run queues that Library's
// precompute jobs.
func TestLibraryPrecomputeScheduleShipsDisabledAndRunsOnceEnabled(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	path := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(path, []byte("beamfall bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ImportMediaFile(ctx, store.ImportMediaFile{
		LibraryID: "library-local", Path: path, Title: "Source", Container: "mkv", Codec: "hevc",
		Class: store.MediaClassVideo, ContentFingerprint: "fp-schedule-precompute",
		DurationSecondsHint: 120, VideoWidthHint: 1280, VideoHeightHint: 720,
		Streams: []store.MediaFileStream{{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10"}},
	}); err != nil {
		t.Fatalf("ImportMediaFile: %v", err)
	}
	id := playback.PrecomputeScheduleID("library-local")
	seededAt := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)

	precompute := newLibraryPrecomputeSchedules(st, t.TempDir(), 1<<40)
	precompute.setSealed(func(store.LibraryID) bool { return true })
	service := schedulepkg.NewService(st)
	if err := precompute.registerAll(ctx, service); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := service.Reconcile(ctx, seededAt.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		row, err := st.Schedule(ctx, id)
		if err != nil {
			t.Fatalf("precompute schedule not seeded: %v", err)
		}
		if row.Enabled || row.JobKind != playback.PrecomputeJobKind {
			t.Fatalf("Reconcile %d: schedule = %#v, want disabled precompute", i, row)
		}
	}
	if err := service.Tick(ctx, seededAt.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if jobs := precomputeJobs(t, st); len(jobs) != 0 {
		t.Fatalf("disabled schedule queued %d precompute jobs", len(jobs))
	}

	enabled := true
	if _, err := st.UpdateSchedule(ctx, id, nil, &enabled); err != nil {
		t.Fatal(err)
	}
	if err := service.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(ctx, seededAt.Add(72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A 720p source backs the 720p and 480p rungs.
	if jobs := precomputeJobs(t, st); len(jobs) != 2 {
		t.Fatalf("enabled schedule queued %d precompute jobs, want 2", len(jobs))
	}
}

func TestLibraryPrecomputeScheduleNeedsACacheBudget(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	service := schedulepkg.NewService(st)
	if err := newLibraryPrecomputeSchedules(st, t.TempDir(), 0).registerAll(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(ctx, time.Unix(800, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Schedule(ctx, playback.PrecomputeScheduleID("library-local")); err == nil {
		t.Fatal("precompute schedule seeded without a rendition cache budget")
	}
}

func precomputeJobs(t *testing.T, st *store.Store) []store.Job {
	t.Helper()
	jobs, err := st.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Job
	for _, job := range jobs {
		if job.Kind == playback.PrecomputeJobKind {
			out = append(out, job)
		}
	}
	return out
}
