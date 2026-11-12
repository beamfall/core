// Package watcher observes Library roots and asks the existing scan path to run.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/fsnotify/fsnotify"
)

// EnqueueScan enqueues the existing full-Library scan job for a Library root.
type EnqueueScan func(context.Context, store.LibraryID, string)

// defaultMaxWatchedDirs caps the number of directories a single root may arm a
// native watch handle for. fsnotify opens one OS file handle per watched
// directory (one kqueue fd per dir on darwin); a Downloads-scale tree
// (~26k dirs and growing via Create events) would otherwise drive
// kern.num_files to ENFILE: file table overflow. Once the cap is hit the root
// stops arming new watches and falls back to the periodic Schedule scan, which
// ADR-0032 designates as the correctness backstop. 50k leaves generous headroom
// over real libraries while staying far below the kernel file-table ceiling.
const (
	defaultMaxWatchedDirs = 50_000
	defaultAddTimeout     = time.Second
	// defaultRetryBase and defaultRetryMax bound the capped exponential backoff
	// used to re-establish a root's live watch after the native watcher fails
	// (fatal backend error, timed-out registration, unreadable root). Live
	// watching recovers on its own instead of staying degraded until restart.
	defaultRetryBase = time.Second
	defaultRetryMax  = 5 * time.Minute
)

// Status reports the current watcher state for a Library or root.
type Status struct {
	Active    bool
	Available bool
	Error     string
}

// Options configures the watcher service.
type Options struct {
	Debounce    time.Duration
	NewWatcher  func() (Backend, error)
	EnqueueScan EnqueueScan
	Logger      *slog.Logger
	// MaxWatchedDirs caps the watched-directory count per root (fd-leak guard).
	// Zero uses defaultMaxWatchedDirs.
	MaxWatchedDirs int
	// AddTimeout bounds each native watch registration. Zero uses one second.
	AddTimeout time.Duration
	// Disabled suppresses live watching while leaving scheduled scans untouched.
	Disabled bool
}

// Backend is the native watcher seam.
type Backend interface {
	Add(string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type watchBackend = Backend

type backend struct {
	*fsnotify.Watcher
}

func (b backend) Events() <-chan fsnotify.Event { return b.Watcher.Events }
func (b backend) Errors() <-chan error          { return b.Watcher.Errors }

// Service manages root watchers and debounce timers.
type Service struct {
	mu       sync.Mutex
	roots    map[rootKey]*rootState
	debounce time.Duration
	newWatch func() (Backend, error)
	enqueue  EnqueueScan
	logger   *slog.Logger
	maxWatch int
	addWait  time.Duration
	// retryBase/retryMax are the restart backoff bounds; tests shrink them.
	retryBase time.Duration
	retryMax  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	closed    bool
	disabled  bool
}

type rootKey struct {
	libraryID store.LibraryID
	root      string
}

type rootState struct {
	key       rootKey
	backend   Backend
	debounce  time.Duration
	enqueue   EnqueueScan
	logger    *slog.Logger
	maxWatch  int
	addWait   time.Duration
	newWatch  func() (Backend, error)
	retryBase time.Duration
	retryMax  time.Duration
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	timer     *time.Timer
	active    bool
	available bool
	starting  bool
	err       string
	closed    bool
	timedOut  bool
	// watched is the set of directories this root has armed a native watch
	// for. It dedupes Add calls (handleEvent re-walks subtrees on every Create
	// event) and enforces maxWatch so a huge tree can't exhaust the kernel
	// file table. capWarned ensures the cap-reached warning logs once.
	watched           map[string]struct{}
	capWarned         bool
	unavailableLogged bool
	// retry is the pending backoff restart, if any; failures counts
	// consecutive failed (re)starts and drives the backoff exponent.
	retry    *time.Timer
	failures int
	// rearming is set while an overflow recovery walk is in flight.
	rearming bool
}

// NewService creates a watcher service. A nil logger defaults to slog.Default().
func NewService(opts Options) *Service {
	if opts.Debounce <= 0 {
		opts.Debounce = 100 * time.Millisecond
	}
	if opts.NewWatcher == nil {
		opts.NewWatcher = func() (Backend, error) {
			w, err := fsnotify.NewWatcher()
			if err != nil {
				return nil, err
			}
			return backend{Watcher: w}, nil
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.MaxWatchedDirs <= 0 {
		opts.MaxWatchedDirs = defaultMaxWatchedDirs
	}
	if opts.AddTimeout <= 0 {
		opts.AddTimeout = defaultAddTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		roots:     map[rootKey]*rootState{},
		debounce:  opts.Debounce,
		newWatch:  opts.NewWatcher,
		enqueue:   opts.EnqueueScan,
		logger:    opts.Logger,
		maxWatch:  opts.MaxWatchedDirs,
		addWait:   opts.AddTimeout,
		retryBase: defaultRetryBase,
		retryMax:  defaultRetryMax,
		disabled:  opts.Disabled,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Observe starts watching one Library root if it is not already tracked.
func (s *Service) Observe(libraryID store.LibraryID, root string) Status {
	root = filepath.Clean(root)
	key := rootKey{libraryID: libraryID, root: root}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Status{Error: "watcher service closed"}
	}
	if s.disabled {
		s.mu.Unlock()
		return Status{Error: "live watcher disabled; using scheduled scans"}
	}
	if state, ok := s.roots[key]; ok {
		status := state.status()
		s.mu.Unlock()
		if status.Active {
			return status
		}
		if state.addTimedOut() {
			// A timed-out registration is retried in the background with
			// backoff; never block Observe on the same stalled filesystem.
			return status
		}
		return state.startOrRetry()
	}
	state := &rootState{
		key:       key,
		debounce:  s.debounce,
		enqueue:   s.enqueue,
		logger:    s.logger,
		maxWatch:  s.maxWatch,
		addWait:   s.addWait,
		newWatch:  s.newWatch,
		retryBase: s.retryBase,
		retryMax:  s.retryMax,
		ctx:       s.ctx,
		watched:   map[string]struct{}{},
	}
	state.ctx, state.cancel = context.WithCancel(s.ctx)
	s.roots[key] = state
	s.mu.Unlock()

	return state.startOrRetry()
}

// Status reports the aggregate watcher state for a Library.
func (s *Service) Status(libraryID store.LibraryID) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return Status{Error: "live watcher disabled; using scheduled scans"}
	}
	var aggregate Status
	for key, state := range s.roots {
		if key.libraryID != libraryID {
			continue
		}
		st := state.status()
		if st.Active {
			return st
		}
		if st.Available {
			aggregate.Available = true
		}
		if st.Error != "" {
			aggregate.Error = st.Error
		}
	}
	return aggregate
}

// Close stops every tracked watcher.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	var errs []error
	for key, state := range s.roots {
		if err := state.close(); err != nil {
			errs = append(errs, err)
		}
		delete(s.roots, key)
	}
	return errors.Join(errs...)
}

// startOrRetry starts the root's watch now and, on failure, marks it
// unavailable and schedules a background restart with backoff.
func (s *rootState) startOrRetry() Status {
	if err := s.start(); err != nil {
		status := s.markUnavailable(err)
		s.scheduleRestart()
		return status
	}
	return s.status()
}

func (s *rootState) start() error {
	s.mu.Lock()
	if s.closed || s.active || s.starting {
		s.mu.Unlock()
		return nil
	}
	s.starting = true
	s.mu.Unlock()

	backend, err := s.newWatch()
	if err != nil {
		s.mu.Lock()
		s.starting = false
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.starting = false
		s.mu.Unlock()
		_ = backend.Close()
		return nil
	}
	s.backend = backend
	s.watched = map[string]struct{}{}
	s.mu.Unlock()
	if err := s.addTree(s.key.root); err != nil {
		_ = backend.Close()
		s.mu.Lock()
		s.backend = nil
		s.watched = map[string]struct{}{}
		s.starting = false
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.starting = false
		s.mu.Unlock()
		_ = backend.Close()
		return nil
	}
	s.active = true
	s.available = true
	s.starting = false
	s.err = ""
	s.timedOut = false
	s.failures = 0
	recovered := s.unavailableLogged
	// Re-arm the degrade warning so a later failure is reported again.
	s.unavailableLogged = false
	s.mu.Unlock()
	if recovered {
		s.logger.Info("live watch restored", "library", string(s.key.libraryID), "root", s.key.root)
	}
	go s.run(backend)
	return nil
}

// scheduleRestart arms one backoff timer that re-establishes the watch. The
// delay doubles per consecutive failure from retryBase up to retryMax.
func (s *rootState) scheduleRestart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.active || s.retry != nil || s.ctx.Err() != nil {
		return
	}
	delay := s.retryBase
	for i := 0; i < s.failures && delay < s.retryMax; i++ {
		delay *= 2
	}
	if delay > s.retryMax {
		delay = s.retryMax
	}
	s.failures++
	s.retry = time.AfterFunc(delay, s.retryStart)
}

func (s *rootState) retryStart() {
	s.mu.Lock()
	s.retry = nil
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.startOrRetry()
}

func (s *rootState) run(backend Backend) {
	// Pin the backend this goroutine was started for, and close it on every
	// exit path: markUnavailable only flips flags, and a subsequent Observe
	// overwrites s.backend — without the close here each error/re-observe
	// cycle would leak the old watcher's descriptors and goroutine.
	defer func() { _ = backend.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case event, ok := <-backend.Events():
			if !ok {
				s.fail(backend, errors.New("watcher closed"))
				return
			}
			s.handleEvent(event)
		case err, ok := <-backend.Errors():
			if !ok {
				s.fail(backend, errors.New("watcher error channel closed"))
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// The kernel dropped events (e.g. a large copy) but the
				// watches are intact: keep running and recover what was lost.
				s.recoverOverflow()
				continue
			}
			s.fail(backend, err)
			return
		}
	}
}

// fail degrades a running root after its backend died and schedules a
// backoff restart so live watching comes back without operator action.
func (s *rootState) fail(backend Backend, err error) {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if s.backend == backend {
		s.backend = nil
		s.watched = map[string]struct{}{}
	}
	s.mu.Unlock()
	s.markUnavailable(err)
	s.scheduleRestart()
}

// recoverOverflow re-arms directories whose Create events were lost and then
// asks for a scan of the root, since any media event may have been dropped.
// At most one recovery walk runs at a time so the event loop keeps draining.
func (s *rootState) recoverOverflow() {
	s.mu.Lock()
	if s.closed || s.rearming {
		s.mu.Unlock()
		return
	}
	s.rearming = true
	s.mu.Unlock()
	s.logger.Info("live watch event queue overflowed; rescanning root", "library", string(s.key.libraryID), "root", s.key.root)
	go func() {
		defer func() {
			s.mu.Lock()
			s.rearming = false
			s.mu.Unlock()
		}()
		if err := s.addTree(s.key.root); err != nil {
			s.logger.Debug("overflow re-arm walk failed", "library", string(s.key.libraryID), "root", s.key.root, "reason", err)
		}
		s.trigger()
	}()
}

func (s *rootState) handleEvent(event fsnotify.Event) {
	if event.Name == "" {
		return
	}
	if event.Op&fsnotify.Create != 0 {
		path := event.Name
		if resolved, err := filepath.EvalSymlinks(event.Name); err == nil {
			path = resolved
		}
		// A subtree that vanished or cannot be read must not affect the
		// root; a timed-out registration closes the backend and run's
		// restart path takes over.
		if err := s.addTree(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.logger.Debug("watch for new directory failed", "library", string(s.key.libraryID), "root", s.key.root, "path", path, "reason", err)
		}
	}
	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		s.forgetTree(event.Name)
		// Create events arm the EvalSymlinks-resolved path, but the removed
		// path itself no longer resolves — prune the parent-resolved form too
		// (e.g. macOS /var/... events for watches armed under /private/var/...).
		if parent, err := filepath.EvalSymlinks(filepath.Dir(event.Name)); err == nil {
			s.forgetTree(filepath.Join(parent, filepath.Base(event.Name)))
		}
	}
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) == 0 {
		return
	}
	s.trigger()
}

// forgetTree drops path and its descendants from the watched set. The native
// backend already released those watches when the directory vanished; without
// the prune, addTree's dedup would skip re-arming a directory recreated at the
// same path, silently downgrading that subtree to the periodic Schedule scan.
func (s *rootState) forgetTree(path string) {
	path = filepath.Clean(path)
	prefix := path + string(filepath.Separator)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.watched, path)
	for watched := range s.watched {
		if strings.HasPrefix(watched, prefix) {
			delete(s.watched, watched)
		}
	}
}

func (s *rootState) trigger() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(s.debounce, s.fire)
		return
	}
	if !s.timer.Stop() {
		// Timer callbacks may already be running; Reset arms the next quiet period.
	}
	s.timer.Reset(s.debounce)
}

func (s *rootState) fire() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	enqueue := s.enqueue
	key := s.key
	ctx := s.ctx
	s.mu.Unlock()
	if enqueue != nil {
		enqueue(ctx, key.libraryID, key.root)
	}
}

func (s *rootState) markUnavailable(err error) Status {
	s.mu.Lock()
	s.available = false
	s.active = false
	if err != nil {
		s.err = err.Error()
		s.timedOut = errors.Is(err, context.DeadlineExceeded)
	}
	shouldLog := !s.unavailableLogged
	s.unavailableLogged = true
	status := s.snapshotLocked()
	s.mu.Unlock()
	if shouldLog {
		s.logger.Warn("live watch unavailable; using scheduled scans", "library", string(s.key.libraryID), "root", s.key.root, "reason", err)
	}
	return status
}

func (s *rootState) addTimedOut() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.timedOut
}

func (s *rootState) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *rootState) snapshotLocked() Status {
	return Status{
		Active:    s.active,
		Available: s.available,
		Error:     s.err,
	}
}

func (s *rootState) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.active = false
	s.available = false
	s.starting = false
	s.err = "watcher closed"
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.retry != nil {
		s.retry.Stop()
		s.retry = nil
	}
	backend := s.backend
	s.backend = nil
	s.watched = map[string]struct{}{}
	s.mu.Unlock()
	s.cancel()
	if backend == nil {
		return nil
	}
	return backend.Close()
}

// addTree walks root and arms a native watch on each directory not already
// watched, deduping via s.watched so repeated walks (handleEvent re-walks on
// every Create event) never re-Add a path. It stops arming new watches once
// s.maxWatch directories are armed, logging a single warning, so a
// Downloads-scale tree cannot exhaust the kernel file table. Directories
// beyond the cap fall back to the Schedule scan (ADR-0032 backstop).
//
// A subdirectory that cannot be read or watched because it is unreadable or
// already gone (lost+found, a NAS #recycle folder) is skipped so the rest of
// the tree stays live; only a failure on root itself is returned.
func (s *rootState) addTree(root string) error {
	root = filepath.Clean(root)
	skippable := func(path string, err error) bool {
		if filepath.Clean(path) == root || !(errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist)) {
			return false
		}
		s.logger.Debug("skipping unwatchable directory", "library", string(s.key.libraryID), "root", s.key.root, "path", path, "reason", err)
		return true
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if skippable(path, err) {
				if entry != nil && !entry.IsDir() {
					return nil
				}
				return fs.SkipDir
			}
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		path = filepath.Clean(path)

		s.mu.Lock()
		if s.closed || s.backend == nil {
			s.mu.Unlock()
			return filepath.SkipAll
		}
		if _, ok := s.watched[path]; ok {
			s.mu.Unlock()
			return nil
		}
		if len(s.watched) >= s.maxWatch {
			if !s.capWarned {
				s.capWarned = true
				s.logger.Warn("watched-directory cap reached; new directories will rely on the periodic Schedule scan",
					"library", string(s.key.libraryID), "root", s.key.root, "cap", s.maxWatch)
			}
			s.mu.Unlock()
			return filepath.SkipAll
		}
		backend := s.backend
		s.mu.Unlock()

		if err := addWithTimeout(s.ctx, backend, path, s.addWait); err != nil {
			if skippable(path, err) {
				return fs.SkipDir
			}
			return err
		}
		s.mu.Lock()
		if s.closed || s.backend == nil {
			s.mu.Unlock()
			return filepath.SkipAll
		}
		s.watched[path] = struct{}{}
		s.mu.Unlock()
		return nil
	})
}

func addWithTimeout(ctx context.Context, backend Backend, path string, timeout time.Duration) error {
	result := make(chan error, 1)
	go func() { result <- backend.Add(path) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		_ = backend.Close()
		return ctx.Err()
	case <-timer.C:
		_ = backend.Close()
		return fmt.Errorf("watch registration for %q timed out after %s: %w", path, timeout, context.DeadlineExceeded)
	}
}
