package watcher

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/beamfall/core/internal/store"
)

// A deleted Library must stop raising scan jobs at once, and a restored one
// must be watchable again: Forget drops every root of one Library and leaves
// the others alone.
func TestForgetStopsOneLibraryAndObserveResumesIt(t *testing.T) {
	filmsRoot, extrasRoot, otherRoot := t.TempDir(), t.TempDir(), t.TempDir()
	var backends []*countingBackend
	var scans atomic.Int32
	service := NewService(Options{
		Debounce: 10 * time.Millisecond,
		NewWatcher: func() (watchBackend, error) {
			be := newCountingBackend()
			backends = append(backends, be)
			return be, nil
		},
		EnqueueScan: func(context.Context, store.LibraryID, string) { scans.Add(1) },
	})
	t.Cleanup(func() { _ = service.Close() })

	for _, observed := range []struct {
		library store.LibraryID
		root    string
	}{{"library-films", filmsRoot}, {"library-films", extrasRoot}, {"library-other", otherRoot}} {
		if status := service.Observe(observed.library, observed.root); !status.Active {
			t.Fatalf("Observe(%s, %s) = %#v, want active", observed.library, observed.root, status)
		}
	}
	filmsState, _ := service.rootStateForTest("library-films", filmsRoot)

	if err := service.Forget("library-films"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if status := service.Status("library-films"); status.Active || status.Available {
		t.Fatalf("status after Forget = %#v, want no watched root", status)
	}
	if got := service.watchedCount("library-films", filmsRoot) + service.watchedCount("library-films", extrasRoot); got != 0 {
		t.Fatalf("watched directories after Forget = %d, want 0", got)
	}
	if got := backends[0].liveCount() + backends[1].liveCount(); got != 0 {
		t.Fatalf("native watches still held after Forget = %d, want 0", got)
	}
	if status := service.Status("library-other"); !status.Active {
		t.Fatalf("other library status after Forget = %#v, want still active", status)
	}

	// An event that was already in flight for the forgotten root raises no scan.
	filmsState.handleEvent(fsnotify.Event{Name: filmsRoot + "/new.mp4", Op: fsnotify.Create})
	time.Sleep(60 * time.Millisecond)
	if got := scans.Load(); got != 0 {
		t.Fatalf("scans raised for a forgotten library = %d, want 0", got)
	}

	if status := service.Observe("library-films", filmsRoot); !status.Active {
		t.Fatalf("Observe after Forget = %#v, want active", status)
	}
	if status := service.Status("library-films"); !status.Active {
		t.Fatalf("status after re-Observe = %#v, want active", status)
	}
	// Forgetting a Library that is not watched is a no-op.
	if err := service.Forget("library-unknown"); err != nil {
		t.Fatalf("Forget(unknown): %v", err)
	}
}

// A folder removed from a Library stops being watched while the Library's
// other folders, and other Libraries, keep their watches (CORE-1280).
func TestSyncRootsStopsRemovedRootsOnly(t *testing.T) {
	filmsRoot, extrasRoot, otherRoot := t.TempDir(), t.TempDir(), t.TempDir()
	var scans atomic.Int32
	service := NewService(Options{
		Debounce:    10 * time.Millisecond,
		NewWatcher:  func() (watchBackend, error) { return newCountingBackend(), nil },
		EnqueueScan: func(context.Context, store.LibraryID, string) { scans.Add(1) },
	})
	t.Cleanup(func() { _ = service.Close() })
	service.Observe("library-films", filmsRoot)
	service.Observe("library-films", extrasRoot)
	service.Observe("library-other", extrasRoot)
	service.Observe("library-other", otherRoot)
	extrasState, _ := service.rootStateForTest("library-films", extrasRoot)

	if err := service.SyncRoots("library-films", func() ([]string, error) { return []string{filmsRoot + "/"}, nil }); err != nil {
		t.Fatalf("SyncRoots: %v", err)
	}
	if _, ok := service.rootStateForTest("library-films", extrasRoot); ok {
		t.Fatal("removed root is still watched")
	}
	for _, kept := range []struct {
		library store.LibraryID
		root    string
	}{{"library-films", filmsRoot}, {"library-other", extrasRoot}, {"library-other", otherRoot}} {
		if state, ok := service.rootStateForTest(kept.library, kept.root); !ok || !state.status().Active {
			t.Fatalf("%s %s lost its watch", kept.library, kept.root)
		}
	}
	// A scan that resolved the removed folder before the edit cannot re-arm it,
	// while a folder under a kept root still can be watched.
	if status := service.Observe("library-films", extrasRoot); status.Active {
		t.Fatalf("Observe of a removed root = %#v, want refused", status)
	}
	if _, ok := service.rootStateForTest("library-films", extrasRoot); ok {
		t.Fatal("Observe re-armed a removed root")
	}
	if status := service.Observe("library-films", filepath.Join(filmsRoot, "Sub")); status.Error == "folder is not part of the library" {
		t.Fatalf("Observe under a kept root = %#v, want allowed", status)
	}
	extrasState.handleEvent(fsnotify.Event{Name: extrasRoot + "/new.mp4", Op: fsnotify.Create})
	time.Sleep(60 * time.Millisecond)
	if got := scans.Load(); got != 0 {
		t.Fatalf("scans raised for a removed root = %d, want 0", got)
	}
}
