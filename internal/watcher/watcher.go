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
	ctx      context.Context
	cancel   context.CancelFunc
	closed   bool
	disabled bool
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
		roots:    map[rootKey]*rootState{},
		debounce: opts.Debounce,
		newWatch: opts.NewWatcher,
		enqueue:  opts.EnqueueScan,
		logger:   opts.Logger,
		maxWatch: opts.MaxWatchedDirs,
		addWait:  opts.AddTimeout,
		disabled: opts.Disabled,
		ctx:      ctx,
		cancel:   cancel,
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
			return status
		}
		if err := state.start(s.newWatch); err != nil {
			return state.markUnavailable(err)
		}
		return state.status()
	}
	state := &rootState{
		key:      key,
		debounce: s.debounce,
		enqueue:  s.enqueue,
		logger:   s.logger,
		maxWatch: s.maxWatch,
		addWait:  s.addWait,
		ctx:      s.ctx,
		watched:  map[string]struct{}{},
	}
	state.ctx, state.cancel = context.WithCancel(s.ctx)
	s.roots[key] = state
	s.mu.Unlock()

	if err := state.start(s.newWatch); err != nil {
		return state.markUnavailable(err)
	}
	return state.status()
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

func (s *rootState) start(newWatch func() (Backend, error)) error {
	s.mu.Lock()
	if s.closed || s.active || s.starting {
		s.mu.Unlock()
		return nil
	}
	s.starting = true
	s.mu.Unlock()

	backend, err := newWatch()
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
	s.mu.Unlock()
	go s.run(backend)
	return nil
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
				s.markUnavailable(errors.New("watcher closed"))
				return
			}
			s.handleEvent(event)
		case err, ok := <-backend.Errors():
			if !ok {
				s.markUnavailable(errors.New("watcher error channel closed"))
				return
			}
			s.markUnavailable(err)
			return
		}
	}
}

func (s *rootState) handleEvent(event fsnotify.Event) {
	if event.Name == "" {
		return
	}
	if event.Op&fsnotify.Create != 0 {
		if info, err := filepath.EvalSymlinks(event.Name); err == nil {
			_ = s.addTree(info)
		} else {
			_ = s.addTree(event.Name)
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
func (s *rootState) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
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
