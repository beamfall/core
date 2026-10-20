package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// feature:watcher — regression for the never-pruned watched set:
// a directory removed and later recreated at the same path must be re-armed,
// and delete churn must keep watchedCount bounded to the live directory count
// rather than growing with every directory ever seen.
func TestWatcherForgetsRemovedDirsAndRearmsOnRecreate(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "album")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	be := newCountingBackend()
	service := NewService(Options{
		Debounce:   25 * time.Millisecond,
		NewWatcher: func() (watchBackend, error) { return be, nil },
	})
	t.Cleanup(func() { _ = service.Close() })

	if status := service.Observe("library-local", root); !status.Active {
		t.Fatalf("status after Observe = %#v, want active", status)
	}
	if got := service.watchedCount("library-local", root); got != 2 {
		t.Fatalf("watchedCount after Observe = %d, want 2 (root + album)", got)
	}
	state, ok := service.rootStateForTest("library-local", root)
	if !ok {
		t.Fatal("rootStateForTest: root not tracked")
	}

	// Delete the subtree, deliver the Remove event: the watched set must prune
	// the dead entry instead of retaining it forever.
	if err := os.RemoveAll(sub); err != nil {
		t.Fatalf("remove: %v", err)
	}
	state.handleEvent(fsnotify.Event{Name: sub, Op: fsnotify.Remove})
	if got := service.watchedCount("library-local", root); got != 1 {
		t.Fatalf("watchedCount after Remove = %d, want 1 (album pruned)", got)
	}

	// Recreate the same path and deliver the Create event: without the prune,
	// addTree's dedup would skip re-arming and the subtree would silently fall
	// back to the periodic Schedule scan.
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	state.handleEvent(fsnotify.Event{Name: sub, Op: fsnotify.Create})
	if got := service.watchedCount("library-local", root); got != 2 {
		t.Fatalf("watchedCount after recreate = %d, want 2 (album re-armed)", got)
	}

	// Churn: create+remove fresh directories; the watched set must track the
	// live count, not the cumulative created count.
	for i := 0; i < 10; i++ {
		churn := filepath.Join(root, "churn")
		if err := os.MkdirAll(churn, 0o755); err != nil {
			t.Fatalf("churn mkdir %d: %v", i, err)
		}
		state.handleEvent(fsnotify.Event{Name: churn, Op: fsnotify.Create})
		if err := os.RemoveAll(churn); err != nil {
			t.Fatalf("churn remove %d: %v", i, err)
		}
		state.handleEvent(fsnotify.Event{Name: churn, Op: fsnotify.Remove})
	}
	if got := service.watchedCount("library-local", root); got != 2 {
		t.Fatalf("watchedCount after churn = %d, want 2 (bounded to live dirs)", got)
	}
}
