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
	service.setFlapLimitsForTest(time.Minute, 0) // every outage is reported
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

const (
	degradeLine  = "live watch unavailable; using scheduled scans"
	restoredLine = "live watch restored"
	overflowLine = "live watch event queue overflowed"
)

func rootStateFor(t *testing.T, service *Service, libraryID store.LibraryID, root string) *rootState {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	state := service.roots[rootKey{libraryID: libraryID, root: filepath.Clean(root)}]
	if state == nil {
		t.Fatalf("no watcher state for %s", root)
	}
	return state
}

// flap kills the watch and returns the backend that replaces it and how long
// the replacement took.
func flap(t *testing.T, service *Service, factory *backendFactory, be *scriptedBackend) (*scriptedBackend, time.Duration) {
	t.Helper()
	killed := time.Now() // before the send, so waited can only overstate the delay
	be.errs <- errors.New("watch descriptor lost")
	next := waitBackend(t, factory.madeCh)
	waited := time.Since(killed)
	waitFor(t, "watch restored", func() bool { return service.Status("library-local").Active })
	return next, waited
}

func TestFlappingWatchBacksOffAndWarnsOnce(t *testing.T) {
	// feature:watcher: CORE-1170. A watch that comes up and dies again at
	// once must climb the backoff to its cap and write one warning, not restart
	// every retryBase with a warning each time.
	root := t.TempDir()
	logs := &syncBuffer{}
	factory := newBackendFactory()
	service := NewService(Options{NewWatcher: factory.newWatcher, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	const base, limit = 5 * time.Millisecond, 80 * time.Millisecond
	service.setRetryBackoffForTest(base, limit)
	service.setFlapLimitsForTest(time.Hour, time.Hour)
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	be := waitBackend(t, factory.madeCh)
	want := base
	for i := range 7 {
		var waited time.Duration
		be, waited = flap(t, service, factory, be)
		// A timer never fires early, so the delay is a hard lower bound.
		if waited < want {
			t.Fatalf("restart %d came after %v, want at least %v", i+1, waited, want)
		}
		want = min(want*2, limit)
	}
	if want != limit {
		t.Fatalf("test never reached the backoff cap: %v", want)
	}
	if got := logs.count(degradeLine); got != 1 {
		t.Fatalf("degrade warnings = %d, want 1 for a flapping root", got)
	}
	if got := logs.count(restoredLine); got != 1 {
		t.Fatalf("restored lines = %d, want 1", got)
	}
}

func TestStableWatchResetsBackoff(t *testing.T) {
	root := t.TempDir()
	factory := newBackendFactory()
	service := NewService(Options{NewWatcher: factory.newWatcher})
	service.setRetryBackoffForTest(time.Millisecond, time.Hour)
	service.setFlapLimitsForTest(time.Hour, time.Hour)
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	be := waitBackend(t, factory.madeCh)
	state := rootStateFor(t, service, "library-local", root)
	for range 3 {
		be, _ = flap(t, service, factory, be)
	}
	state.mu.Lock()
	climbed := state.failures
	// The current watch has now been up for longer than the stability window.
	state.startedAt = time.Now().Add(-2 * time.Hour)
	state.mu.Unlock()
	if climbed != 3 {
		t.Fatalf("failures after three flaps = %d, want 3", climbed)
	}

	flap(t, service, factory, be)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.failures != 1 {
		t.Fatalf("failures after a stable watch failed = %d, want 1 (backoff restarted)", state.failures)
	}
}

func TestDegradeWarningCarriesSuppressedCountAfterInterval(t *testing.T) {
	root := t.TempDir()
	logs := &syncBuffer{}
	factory := newBackendFactory()
	service := NewService(Options{NewWatcher: factory.newWatcher, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	service.setRetryBackoffForTest(time.Millisecond, 2*time.Millisecond)
	service.setFlapLimitsForTest(time.Hour, time.Hour)
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	be := waitBackend(t, factory.madeCh)
	for range 3 { // one warning, two suppressed
		be, _ = flap(t, service, factory, be)
	}
	if got := logs.count(degradeLine); got != 1 {
		t.Fatalf("degrade warnings inside the interval = %d, want 1", got)
	}

	state := rootStateFor(t, service, "library-local", root)
	state.mu.Lock()
	state.lastWarn = time.Now().Add(-2 * time.Hour) // the interval has passed
	state.mu.Unlock()
	flap(t, service, factory, be)
	if got := logs.count(degradeLine); got != 2 {
		t.Fatalf("degrade warnings after the interval = %d, want 2", got)
	}
	if got := logs.count("failures_since_last_warning=2"); got != 1 {
		t.Fatalf("suppressed count missing from the second warning: %d", got)
	}
	if got := logs.count(restoredLine); got != 2 {
		t.Fatalf("restored lines = %d, want one per written warning", got)
	}
}

func TestRepeatedStartFailuresWarnOncePerOutage(t *testing.T) {
	root := t.TempDir()
	logs := &syncBuffer{}
	factory := newBackendFactory()
	factory.failures = 4
	service := NewService(Options{NewWatcher: factory.newWatcher, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	service.setRetryBackoffForTest(time.Millisecond, 2*time.Millisecond)
	service.setFlapLimitsForTest(time.Hour, 0) // even with no rate limit
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	waitBackend(t, factory.madeCh)
	waitFor(t, "watch established", func() bool { return service.Status("library-local").Active })
	if got := logs.count(degradeLine); got != 1 {
		t.Fatalf("degrade warnings for one outage of four failed starts = %d, want 1", got)
	}
}

func TestRepeatedOverflowLogsOncePerInterval(t *testing.T) {
	root := t.TempDir()
	logs := &syncBuffer{}
	factory := newBackendFactory()
	enqueue, scans := scanRecorder()
	service := NewService(Options{
		Debounce: time.Millisecond, NewWatcher: factory.newWatcher, EnqueueScan: enqueue,
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	be := waitBackend(t, factory.madeCh)
	state := rootStateFor(t, service, "library-local", root)
	for i := range 3 {
		be.errs <- fsnotify.ErrEventOverflow
		waitScan(t, scans, "overflow rescan") // every overflow is still recovered
		waitFor(t, "recovery walk finished", func() bool {
			state.mu.Lock()
			defer state.mu.Unlock()
			return !state.rearming
		})
		if got := logs.count(overflowLine); got != 1 {
			t.Fatalf("overflow lines after %d overflows = %d, want 1", i+1, got)
		}
	}
}

func TestOverflowDuringRecoveryWalkIsRemembered(t *testing.T) {
	// An overflow that lands while a recovery walk is running may have lost a
	// Create the walk already passed, so it must schedule one more walk rather
	// than be dropped.
	root := t.TempDir()
	factory := newBackendFactory()
	enqueue, scans := scanRecorder()
	service := NewService(Options{Debounce: time.Millisecond, NewWatcher: factory.newWatcher, EnqueueScan: enqueue})
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)
	waitBackend(t, factory.madeCh)
	state := rootStateFor(t, service, "library-local", root)

	state.mu.Lock()
	state.rearming = true // a walk is "in flight"
	state.mu.Unlock()
	state.recoverOverflow()
	state.mu.Lock()
	again := state.rearmAgain
	state.mu.Unlock()
	if !again {
		t.Fatal("overflow during a recovery walk was dropped; want a repeat walk recorded")
	}

	// Run the loop for real: a repeat request is consumed and recovery ends.
	state.mu.Lock()
	state.rearming = false
	state.rearmAgain = false
	state.mu.Unlock()
	state.recoverOverflow()
	waitScan(t, scans, "recovery rescan")
	waitFor(t, "recovery walk finished", func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return !state.rearming && !state.rearmAgain
	})
}
