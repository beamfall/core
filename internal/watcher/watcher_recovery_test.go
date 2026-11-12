package watcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/fsnotify/fsnotify"
)

// scriptedBackend is a Backend whose events and errors the test injects.
type scriptedBackend struct {
	mu     sync.Mutex
	added  map[string]struct{}
	closed bool
	events chan fsnotify.Event
	errs   chan error
}

func newScriptedBackend() *scriptedBackend {
	return &scriptedBackend{added: map[string]struct{}{}, events: make(chan fsnotify.Event), errs: make(chan error)}
}

func (b *scriptedBackend) Add(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("closed")
	}
	b.added[path] = struct{}{}
	return nil
}

func (b *scriptedBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.events)
		close(b.errs)
	}
	return nil
}

func (b *scriptedBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *scriptedBackend) Errors() <-chan error          { return b.errs }

func (b *scriptedBackend) hasAdded(path string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.added[path]
	return ok
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) count(substr string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), substr)
}

// backendFactory hands out scripted backends and records each construction.
type backendFactory struct {
	mu       sync.Mutex
	made     []*scriptedBackend
	madeCh   chan *scriptedBackend
	failures int // leading constructions that return an error
}

func newBackendFactory() *backendFactory {
	return &backendFactory{madeCh: make(chan *scriptedBackend, 16)}
}

func (f *backendFactory) newWatcher() (Backend, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failures > 0 {
		f.failures--
		return nil, errors.New("root unavailable")
	}
	be := newScriptedBackend()
	f.made = append(f.made, be)
	f.madeCh <- be
	return be, nil
}

func (f *backendFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.made)
}

func waitBackend(t *testing.T, ch <-chan *scriptedBackend) *scriptedBackend {
	t.Helper()
	select {
	case be := <-ch:
		return be
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a watcher backend")
		return nil
	}
}

func waitScan(t *testing.T, scans <-chan struct{}, why string) {
	t.Helper()
	select {
	case <-scans:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for scan: %s", why)
	}
}

func waitFor(t *testing.T, why string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", why)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func scanRecorder() (EnqueueScan, <-chan struct{}) {
	scans := make(chan struct{}, 16)
	return func(context.Context, store.LibraryID, string) {
		select {
		case scans <- struct{}{}:
		default:
		}
	}, scans
}

func TestOverflowRescansRootAndKeepsWatching(t *testing.T) {
	// feature:watcher — an event-queue overflow (large copy) must not end live
	// watching: lost events are recovered by a rescan and later events flow.
	root := t.TempDir()
	factory := newBackendFactory()
	enqueue, scans := scanRecorder()
	service := NewService(Options{Debounce: 10 * time.Millisecond, NewWatcher: factory.newWatcher, EnqueueScan: enqueue})
	t.Cleanup(func() { _ = service.Close() })

	if status := service.Observe("library-local", root); !status.Active {
		t.Fatalf("status = %#v, want active", status)
	}
	be := waitBackend(t, factory.madeCh)

	// A directory created while events were being dropped must get armed.
	lost := filepath.Join(root, "copied-during-overflow")
	if err := os.Mkdir(lost, 0o755); err != nil {
		t.Fatal(err)
	}
	be.errs <- fsnotify.ErrEventOverflow
	waitScan(t, scans, "overflow rescan")
	waitFor(t, "lost directory re-armed", func() bool { return be.hasAdded(lost) })

	if status := service.Status("library-local"); !status.Active || !status.Available {
		t.Fatalf("status after overflow = %#v, want still active", status)
	}
	be.events <- fsnotify.Event{Name: filepath.Join(root, "next.mp4"), Op: fsnotify.Create}
	waitScan(t, scans, "event after overflow")
	if got := factory.count(); got != 1 {
		t.Fatalf("backend constructions = %d, want 1 (overflow must not restart)", got)
	}
}

func TestFatalBackendErrorRestartsWatchWithBackoff(t *testing.T) {
	// feature:watcher — a fatal native watcher error degrades the root, then the
	// watch is re-established on its own and a later failure is logged again.
	root := t.TempDir()
	logs := &syncBuffer{}
	factory := newBackendFactory()
	enqueue, scans := scanRecorder()
	service := NewService(Options{
		Debounce:    10 * time.Millisecond,
		NewWatcher:  factory.newWatcher,
		EnqueueScan: enqueue,
		Logger:      slog.New(slog.NewTextHandler(logs, nil)),
	})
	service.setRetryBackoffForTest(5*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	first := waitBackend(t, factory.madeCh)
	first.errs <- errors.New("inotify: watch descriptor lost")

	second := waitBackend(t, factory.madeCh)
	waitFor(t, "watch restored", func() bool { return service.Status("library-local").Active })
	second.events <- fsnotify.Event{Name: filepath.Join(root, "new.mp4"), Op: fsnotify.Create}
	waitScan(t, scans, "event on restarted watch")

	const degrade = "live watch unavailable; using scheduled scans"
	if got := logs.count(degrade); got != 1 {
		t.Fatalf("degrade log count = %d, want 1", got)
	}
	if got := logs.count("live watch restored"); got != 1 {
		t.Fatalf("restored log count = %d, want 1", got)
	}

	second.errs <- errors.New("inotify: watch descriptor lost again")
	waitBackend(t, factory.madeCh)
	waitFor(t, "watch restored again", func() bool { return service.Status("library-local").Active })
	if got := logs.count(degrade); got != 2 {
		t.Fatalf("degrade log count after second failure = %d, want 2 (re-armed on recovery)", got)
	}
}

func TestStartupFailureIsRetriedInBackground(t *testing.T) {
	root := t.TempDir()
	factory := newBackendFactory()
	factory.failures = 3
	service := NewService(Options{NewWatcher: factory.newWatcher})
	service.setRetryBackoffForTest(time.Millisecond, 4*time.Millisecond)
	t.Cleanup(func() { _ = service.Close() })

	if status := service.Observe("library-local", root); status.Active {
		t.Fatalf("first status = %#v, want unavailable", status)
	}
	waitBackend(t, factory.madeCh)
	waitFor(t, "watch established after retries", func() bool { return service.Status("library-local").Active })
}

func TestTimedOutRootIsRetriedWithBackoff(t *testing.T) {
	root := t.TempDir()
	var mu sync.Mutex
	calls := 0
	made := make(chan *scriptedBackend, 4)
	service := NewService(Options{
		AddTimeout: 20 * time.Millisecond,
		NewWatcher: func() (Backend, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return newIndefinitelyBlockingBackend(), nil
			}
			be := newScriptedBackend()
			made <- be
			return be, nil
		},
	})
	service.setRetryBackoffForTest(5*time.Millisecond, 20*time.Millisecond)
	t.Cleanup(func() { _ = service.Close() })

	if status := service.Observe("library-local", root); status.Active || !strings.Contains(status.Error, "timed out") {
		t.Fatalf("first status = %#v, want timeout", status)
	}
	waitBackend(t, made)
	waitFor(t, "timed-out root re-established", func() bool { return service.Status("library-local").Active })
}

func TestCloseCancelsPendingRestart(t *testing.T) {
	root := t.TempDir()
	factory := newBackendFactory()
	factory.failures = 1
	service := NewService(Options{NewWatcher: factory.newWatcher})
	service.setRetryBackoffForTest(30*time.Millisecond, 30*time.Millisecond)

	service.Observe("library-local", root)
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if got := factory.count(); got != 0 {
		t.Fatalf("backend constructions after Close = %d, want 0", got)
	}
}

func TestUnreadableSubdirectoryDoesNotDisableRoot(t *testing.T) {
	// feature:watcher — one unreadable folder (lost+found, #recycle) must not
	// take live watching away from the rest of the library root.
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "lost+found")
	open := filepath.Join(root, "Shows")
	for _, dir := range []string{locked, open} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	enqueue, scans := scanRecorder()
	service := NewService(Options{Debounce: 10 * time.Millisecond, EnqueueScan: enqueue})
	t.Cleanup(func() { _ = service.Close() })

	if status := service.Observe("library-local", root); !status.Active || !status.Available {
		t.Fatalf("status = %#v, want active despite unreadable subdirectory", status)
	}
	if err := os.WriteFile(filepath.Join(open, "pilot.mp4"), []byte("clip"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitScan(t, scans, "event for sibling file")
}

func TestUnreadableRootStaysUnavailable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := filepath.Join(t.TempDir(), "missing")
	service := NewService(Options{})
	t.Cleanup(func() { _ = service.Close() })
	if status := service.Observe("library-local", root); status.Active || status.Available || status.Error == "" {
		t.Fatalf("status = %#v, want unavailable for a missing root", status)
	}
}
