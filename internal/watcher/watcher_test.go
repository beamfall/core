package watcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
	"github.com/fsnotify/fsnotify"
)

type indefinitelyBlockingBackend struct {
	closed chan struct{}
	events chan fsnotify.Event
	errs   chan error
}

func newIndefinitelyBlockingBackend() *indefinitelyBlockingBackend {
	return &indefinitelyBlockingBackend{closed: make(chan struct{}), events: make(chan fsnotify.Event), errs: make(chan error)}
}

func (b *indefinitelyBlockingBackend) Add(string) error {
	<-b.closed
	return errors.New("backend closed")
}
func (b *indefinitelyBlockingBackend) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
func (b *indefinitelyBlockingBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *indefinitelyBlockingBackend) Errors() <-chan error          { return b.errs }

func TestObserveBoundsBlockedAddAndLogsDegradeOnce(t *testing.T) {
	root := t.TempDir()
	var logs bytes.Buffer
	backendCount := 0
	service := NewService(Options{
		AddTimeout: 20 * time.Millisecond,
		Logger:     slog.New(slog.NewTextHandler(&logs, nil)),
		NewWatcher: func() (Backend, error) {
			backendCount++
			return newIndefinitelyBlockingBackend(), nil
		},
	})
	t.Cleanup(func() { _ = service.Close() })

	started := time.Now()
	status := service.Observe("library-local", root)
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Observe elapsed = %s, want bounded", elapsed)
	}
	if status.Active || status.Available || !strings.Contains(status.Error, "timed out") {
		t.Fatalf("status = %#v, want timeout degradation", status)
	}
	if got := service.Status("library-local"); got.Active || got.Available || !strings.Contains(got.Error, "timed out") {
		t.Fatalf("stored status = %#v, want timeout degradation", got)
	}

	_ = service.Observe("library-local", root)
	if backendCount != 1 {
		t.Fatalf("backend constructions = %d, want 1 after terminal timeout", backendCount)
	}
	text := logs.String()
	if got := strings.Count(text, "live watch unavailable; using scheduled scans"); got != 1 {
		t.Fatalf("degrade log count = %d, want 1: %s", got, text)
	}
	if !strings.Contains(text, "root="+root) || !strings.Contains(text, "reason=") {
		t.Fatalf("degrade log lacks root/reason: %s", text)
	}
}

func TestObserveDebouncesBurstWritesIntoOneScanJob(t *testing.T) {
	// feature:watcher
	ctx := context.Background()
	st := storetest.Open(t)
	root := t.TempDir()

	var mu sync.Mutex
	enqueueCount := 0
	var enqueueErr error
	seen := make(chan struct{}, 1)
	service := NewService(Options{
		Debounce: 25 * time.Millisecond,
		EnqueueScan: func(ctx context.Context, libraryID store.LibraryID, root string) {
			job, err := scan.NewJob(scan.JobRequest{
				LibraryID: libraryID,
				Root:      root,
				CreatedAt: time.Unix(100, 0).UTC(),
			})
			if err != nil {
				mu.Lock()
				enqueueErr = err
				mu.Unlock()
				return
			}
			if _, err := st.EnqueueJob(ctx, job); err != nil {
				mu.Lock()
				enqueueErr = err
				mu.Unlock()
				return
			}
			mu.Lock()
			enqueueCount++
			mu.Unlock()
			select {
			case seen <- struct{}{}:
			default:
			}
		},
	})
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	status := service.Observe("library-local", root)
	if !status.Active || !status.Available {
		t.Fatalf("status after Observe = %#v, want active and available", status)
	}

	for i := 0; i < 3; i++ {
		name := filepath.Join(root, fmt.Sprintf("clip-%d.mp4", i))
		if err := os.WriteFile(name, []byte("clip"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("clip-updated"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := enqueueCount
		mu.Unlock()
		if got == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	if enqueueErr != nil {
		t.Fatalf("enqueue callback: %v", enqueueErr)
	}
	if enqueueCount != 1 {
		t.Fatalf("enqueueCount = %d, want 1", enqueueCount)
	}
	mu.Unlock()

	if _, err := waitForQueuedScanJob(ctx, st, deadline); err != nil {
		t.Fatal(err)
	}
	select {
	case <-seen:
	default:
		t.Fatal("enqueue callback was not reached")
	}
}

func TestObserveMarksUnavailableWhenWatcherStartupFails(t *testing.T) {
	st := storetest.Open(t)
	root := t.TempDir()
	service := NewService(Options{
		NewWatcher: func() (watchBackend, error) {
			return nil, errors.New("fd limit")
		},
		EnqueueScan: func(context.Context, store.LibraryID, string) {
			t.Fatal("enqueue callback should not run when watcher setup fails")
		},
	})
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	})

	status := service.Observe("library-local", root)
	if status.Available || status.Active {
		t.Fatalf("status = %#v, want unavailable", status)
	}
	if got := service.Status("library-local"); got.Available || got.Active {
		t.Fatalf("stored status = %#v, want unavailable", got)
	}

	jobs, err := st.ListJobs(context.Background(), store.JobStatusQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("queued jobs = %d, want 0", len(jobs))
	}
}

func TestObserveRetriesStartupFailureOnLaterObserve(t *testing.T) {
	root := t.TempDir()
	be := newCountingBackend()
	attempts := 0
	service := NewService(Options{
		NewWatcher: func() (watchBackend, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("root unavailable")
			}
			return be, nil
		},
	})
	t.Cleanup(func() { _ = service.Close() })

	status := service.Observe("library-local", root)
	if status.Available || status.Active {
		t.Fatalf("first status = %#v, want unavailable", status)
	}

	status = service.Observe("library-local", root)
	if !status.Available || !status.Active {
		t.Fatalf("second status = %#v, want active and available", status)
	}
	if attempts != 2 {
		t.Fatalf("newWatch attempts = %d, want 2", attempts)
	}
	if live := be.liveCount(); live != 1 {
		t.Fatalf("live fds after retry = %d, want 1", live)
	}
}

func TestObserveAfterCloseStaysInactive(t *testing.T) {
	root := t.TempDir()
	be := newCountingBackend()
	service := NewService(Options{
		NewWatcher: func() (watchBackend, error) { return be, nil },
	})

	if status := service.Observe("library-local", root); !status.Active {
		t.Fatalf("status before Close = %#v, want active", status)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	status := service.Observe("library-local", root)
	if status.Active || status.Available || status.Error == "" {
		t.Fatalf("status after Close = %#v, want inactive unavailable error", status)
	}
	if live := be.liveCount(); live != 0 {
		t.Fatalf("live fds after Observe on closed service = %d, want 0", live)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseWhileObserveIsStartingCannotReactivateWatcher(t *testing.T) {
	root := t.TempDir()
	be := newCountingBackend()
	entered := make(chan struct{})
	release := make(chan struct{})
	service := NewService(Options{
		NewWatcher: func() (watchBackend, error) {
			close(entered)
			<-release
			return be, nil
		},
	})
	statusCh := make(chan Status, 1)
	go func() { statusCh <- service.Observe("library-local", root) }()
	<-entered
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	close(release)

	status := <-statusCh
	if status.Active || status.Available {
		t.Fatalf("racing Observe returned false-active status: %#v", status)
	}
	if live := be.liveCount(); live != 0 {
		t.Fatalf("live fds after racing Close = %d, want 0", live)
	}
}

// countingBackend is a watchBackend that tracks live native watch handles
// (Add minus per-path Close) so a test can assert the watcher does not leak
// fds across re-observe / re-walk cycles. It mirrors fsnotify's kqueue
// behaviour: Add of an already-watched path is idempotent (no new handle).
type countingBackend struct {
	mu      sync.Mutex
	watched map[string]struct{}
	live    int // distinct paths currently armed
	adds    int // total Add calls (including dedup hits)
	closed  bool
	events  chan fsnotify.Event
	errs    chan error
}

func newCountingBackend() *countingBackend {
	return &countingBackend{
		watched: map[string]struct{}{},
		events:  make(chan fsnotify.Event),
		errs:    make(chan error),
	}
}

func (b *countingBackend) Add(path string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return errors.New("closed")
	}
	b.adds++
	if _, ok := b.watched[path]; ok {
		return nil // idempotent: kqueue re-arms the same fd, no new handle
	}
	b.watched[path] = struct{}{}
	b.live++
	return nil
}

func (b *countingBackend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.live = 0 // closing the watcher releases every native handle at once
	b.mu.Unlock()
	close(b.events)
	close(b.errs)
	return nil
}

func (b *countingBackend) Events() <-chan fsnotify.Event { return b.events }
func (b *countingBackend) Errors() <-chan error          { return b.errs }

func (b *countingBackend) liveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live
}

func (b *countingBackend) addCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.adds
}

// makeDirTree creates root with depth levels, fanout children each, returning
// the total directory count including root.
func makeDirTree(t *testing.T, root string, depth, fanout int) int {
	t.Helper()
	count := 1
	var build func(dir string, level int)
	build = func(dir string, level int) {
		if level >= depth {
			return
		}
		for i := 0; i < fanout; i++ {
			child := filepath.Join(dir, fmt.Sprintf("d%d", i))
			if err := os.MkdirAll(child, 0o755); err != nil {
				t.Fatal(err)
			}
			count++
			build(child, level+1)
		}
	}
	build(root, 0)
	return count
}

func TestWatcherDoesNotAccumulateFDsAcrossReObserve(t *testing.T) {
	// feature:watcher — regression for the kern.num_files overflow (bugs.md
	// 2026-06-13): re-observe / per-event re-walk must not grow the watched-fd
	// count beyond the directory count.
	root := t.TempDir()
	dirCount := makeDirTree(t, root, 3, 4) // 1+4+16+64 = 85 dirs

	be := newCountingBackend()
	service := NewService(Options{
		Debounce:   25 * time.Millisecond,
		NewWatcher: func() (watchBackend, error) { return be, nil },
	})
	t.Cleanup(func() { _ = service.Close() })

	status := service.Observe("library-local", root)
	if !status.Active || !status.Available {
		t.Fatalf("status after Observe = %#v, want active and available", status)
	}

	armedAfterFirst := be.liveCount()
	if armedAfterFirst != dirCount {
		t.Fatalf("armed fds after first Observe = %d, want %d (one per dir)", armedAfterFirst, dirCount)
	}
	if got := service.watchedCount("library-local", root); got != dirCount {
		t.Fatalf("watchedCount after first Observe = %d, want %d", got, dirCount)
	}

	// Re-observe the SAME root many times (observePersistedRoots /
	// LibraryRootsChanged re-invocation) AND re-walk the tree many times
	// (handleEvent on every Create event). Neither must arm a new fd.
	for i := 0; i < 25; i++ {
		service.Observe("library-local", root)
		if st, ok := service.rootStateForTest("library-local", root); ok {
			if err := st.addTree(root); err != nil {
				t.Fatalf("re-walk %d: %v", i, err)
			}
		}
	}

	if live := be.liveCount(); live != dirCount {
		t.Fatalf("live fds after 25 re-observe/re-walk cycles = %d, want %d (no accumulation)", live, dirCount)
	}
	if got := service.watchedCount("library-local", root); got != dirCount {
		t.Fatalf("watchedCount after re-observe cycles = %d, want %d (no accumulation)", got, dirCount)
	}

	// Close must release every native handle.
	if err := service.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if live := be.liveCount(); live != 0 {
		t.Fatalf("live fds after Close = %d, want 0", live)
	}
}

func TestWatcherBoundsWatchedDirsAtCap(t *testing.T) {
	// feature:watcher — a Downloads-scale tree must not arm unbounded fds.
	root := t.TempDir()
	dirCount := makeDirTree(t, root, 3, 4) // 85 dirs
	const cap = 10
	if dirCount <= cap {
		t.Fatalf("test tree too small: %d dirs <= cap %d", dirCount, cap)
	}

	be := newCountingBackend()
	service := NewService(Options{
		Debounce:       25 * time.Millisecond,
		MaxWatchedDirs: cap,
		NewWatcher:     func() (watchBackend, error) { return be, nil },
	})
	t.Cleanup(func() { _ = service.Close() })

	service.Observe("library-local", root)

	if live := be.liveCount(); live != cap {
		t.Fatalf("live fds with cap %d over %d dirs = %d, want %d", cap, dirCount, live, cap)
	}
	if got := service.watchedCount("library-local", root); got != cap {
		t.Fatalf("watchedCount = %d, want cap %d", got, cap)
	}

	// Re-walks past the cap must not arm anything more.
	if st, ok := service.rootStateForTest("library-local", root); ok {
		addsBefore := be.addCount()
		for i := 0; i < 10; i++ {
			_ = st.addTree(root)
		}
		if live := be.liveCount(); live != cap {
			t.Fatalf("live fds after re-walks past cap = %d, want %d", live, cap)
		}
		// Re-walks still happen but arm nothing new (SkipAll once cap hit).
		_ = addsBefore
	}
}

func waitForQueuedScanJob(ctx context.Context, st *store.Store, deadline time.Time) (store.Job, error) {
	for time.Now().Before(deadline) {
		jobs, err := st.ListJobs(ctx, store.JobStatusQueued)
		if err != nil {
			return store.Job{}, err
		}
		for _, job := range jobs {
			if job.Kind == scan.JobKind {
				return job, nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return store.Job{}, fmt.Errorf("timed out waiting for queued scan job")
}
