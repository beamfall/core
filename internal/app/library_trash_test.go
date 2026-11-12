package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	scanpkg "github.com/beamfall/core/internal/scan"
	schedulepkg "github.com/beamfall/core/internal/schedule"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/beamfall/core/internal/watcher"
)

// idleWatchBackend accepts every watch and never reports an event, so the
// test observes only what the library observer tells the watcher to do.
type idleWatchBackend struct {
	events chan fsnotify.Event
	errors chan error
}

func newIdleWatchBackend() (watcher.Backend, error) {
	return &idleWatchBackend{events: make(chan fsnotify.Event), errors: make(chan error)}, nil
}

func (b *idleWatchBackend) Add(string) error              { return nil }
func (b *idleWatchBackend) Close() error                  { return nil }
func (b *idleWatchBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *idleWatchBackend) Errors() <-chan error          { return b.errors }

func queuedScanJobs(t *testing.T, st *store.Store, libraryID store.LibraryID) int {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, job := range jobs {
		if job.Kind == scanpkg.JobKind && bytes.Contains(job.Payload, []byte(libraryID)) {
			count++
		}
	}
	return count
}

// CORE-1201 acceptance: the folder watch and the scan schedule of a deleted
// Library stop at delete, without a restart, and resume at restore together
// with a scan of every root.
func TestLibraryObserverStopsAtDeleteAndResumesAtRestore(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Films", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, library.ID, []string{root}); err != nil {
		t.Fatal(err)
	}
	wake := newJobWake()
	scheduler := schedulepkg.NewService(wakingScheduleStore{Store: st, wake: wake})
	watching := watcher.NewService(watcher.Options{
		NewWatcher:  newIdleWatchBackend,
		EnqueueScan: func(context.Context, store.LibraryID, string) {},
	})
	t.Cleanup(func() { _ = watching.Close() })
	observer := libraryMutationObserver{store: st, watcher: watching, schedule: scheduler, wake: wake, logger: discardLogger()}

	// The state a running server is in for a live Library.
	scheduleID := schedulepkg.ScanScheduleID(library.ID)
	started := time.Now().UTC()
	scheduler.Register(scanScheduleSpec(st, library.ID))
	if err := scheduler.Reconcile(ctx, started); err != nil {
		t.Fatal(err)
	}
	observer.LibraryRootsChanged(ctx, library.ID, []string{root})
	if status := watching.Status(library.ID); !status.Active {
		t.Fatalf("watch status of a live library = %#v, want active", status)
	}
	due := started.Add(13 * time.Hour) // past the 12 hour default trigger

	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	observer.LibraryDeleted(ctx, library.ID)

	if status := watching.Status(library.ID); status.Active || status.Available {
		t.Fatalf("watch status after delete = %#v, want no watched root", status)
	}
	row, err := st.Schedule(ctx, scheduleID)
	if err != nil || !row.Orphaned {
		t.Fatalf("schedule after delete = %+v, %v; want orphaned", row, err)
	}
	if err := scheduler.Tick(ctx, due); err != nil {
		t.Fatal(err)
	}
	if _, fired, err := st.LatestScheduleOccurrence(ctx, scheduleID); err != nil || fired {
		t.Fatalf("schedule of a deleted library fired (fired %v, err %v)", fired, err)
	}
	if got := queuedScanJobs(t, st, library.ID); got != 0 {
		t.Fatalf("queued scans of a deleted library = %d, want 0", got)
	}

	if err := st.RestoreLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	observer.LibraryRestored(ctx, library.ID)

	if status := watching.Status(library.ID); !status.Active {
		t.Fatalf("watch status after restore = %#v, want active", status)
	}
	if got := queuedScanJobs(t, st, library.ID); got != 1 {
		t.Fatalf("queued scans after restore = %d, want one rescan of the root", got)
	}
	row, err = st.Schedule(ctx, scheduleID)
	if err != nil || row.Orphaned || !row.Enabled {
		t.Fatalf("schedule after restore = %+v, %v; want live and enabled", row, err)
	}
	if err := scheduler.Tick(ctx, due); err != nil {
		t.Fatal(err)
	}
	if _, fired, err := st.LatestScheduleOccurrence(ctx, scheduleID); err != nil || !fired {
		t.Fatalf("schedule of a restored library did not fire (fired %v, err %v)", fired, err)
	}
}

// A purge leaves nothing running for the Library and tolerates one that was
// never stopped, as after a delete made by an older server process.
func TestLibraryObserverPurgeForgetsWatchAndSchedule(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	library, err := st.CreateLibrary(ctx, "Films", false, "", "profile-default")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := st.SetLibraryRoots(ctx, library.ID, []string{root}); err != nil {
		t.Fatal(err)
	}
	wake := newJobWake()
	scheduler := schedulepkg.NewService(wakingScheduleStore{Store: st, wake: wake})
	watching := watcher.NewService(watcher.Options{
		NewWatcher:  newIdleWatchBackend,
		EnqueueScan: func(context.Context, store.LibraryID, string) {},
	})
	t.Cleanup(func() { _ = watching.Close() })
	observer := libraryMutationObserver{store: st, watcher: watching, schedule: scheduler, wake: wake, logger: discardLogger()}
	scheduleID := schedulepkg.ScanScheduleID(library.ID)
	started := time.Now().UTC()
	scheduler.Register(scanScheduleSpec(st, library.ID))
	if err := scheduler.Reconcile(ctx, started); err != nil {
		t.Fatal(err)
	}
	observer.LibraryRootsChanged(ctx, library.ID, []string{root})

	if err := st.DeleteLibrary(ctx, library.ID); err != nil {
		t.Fatal(err)
	}
	purge, err := st.PurgeLibrary(ctx, library.ID)
	if err != nil {
		t.Fatal(err)
	}
	observer.LibraryPurged(ctx, purge)

	if status := watching.Status(library.ID); status.Active || status.Available {
		t.Fatalf("watch status after purge = %#v, want no watched root", status)
	}
	if _, err := st.Schedule(ctx, scheduleID); err == nil {
		t.Fatal("scan schedule row survived the purge")
	}
	if err := scheduler.Tick(ctx, started.Add(13*time.Hour)); err != nil {
		t.Fatalf("tick after purge: %v", err)
	}
	if got := queuedScanJobs(t, st, library.ID); got != 0 {
		t.Fatalf("queued scans of a purged library = %d, want 0", got)
	}
}
