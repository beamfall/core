package playback

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/store"
)

var sessionDirName = regexp.MustCompile(`^session-(?:[0-9a-f]{16}-)?\d+$`)

var errIncompleteScratchSweep = errors.New("playback: incomplete scratch reconciliation")

type scratchState uint8

const (
	scratchAllocating scratchState = iota
	scratchLive
	scratchPending
	scratchRemoving
	scratchDebt
)

// Sessions.mu protects the path registry and every claim's mutable state.
type scratchPath struct {
	name    string
	library store.LibraryID
	state   scratchState
	users   int
	drained chan struct{}
	cleanup *sessionTeardown
	cutoff  chan struct{}
	retryAt time.Time
	backoff time.Duration
	// An unclassified mkdir collision must be inspected before removal.
	inspectCollision bool
}

// scratchAllocation is the unpublished setup owner while mkdir runs off-lock.
// setupMu admits a single owner and mu protects its revocation.
type scratchAllocation struct {
	key     SessionKey
	revoked bool
}

// SessionsOption configures a session manager before its startup sweep.
type SessionsOption func(*Sessions)

// WithScratchReadDir supplies the directory read used by scratch reconciliation.
// The reader receives the locked root's filesystem, including at startup.
func WithScratchReadDir(read func(fs.FS, string) ([]fs.DirEntry, error)) SessionsOption {
	return func(s *Sessions) { s.readScratch = read }
}

// NewSessions creates the default scratch root on first use.
func NewSessions(transcoder Transcoder, dataDir string, now func() time.Time) (*Sessions, error) {
	dir, err := defaultScratchDir(dataDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return NewSessionsInDir(transcoder, dir, now)
}

// ResolveDefaultScratchDir resolves the existing default root without creating it.
func ResolveDefaultScratchDir(dataDir string) (string, error) {
	dir, err := defaultScratchDir(dataDir)
	if err != nil {
		return "", err
	}
	return resolveScratchDir(dir)
}

func defaultScratchDir(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("playback: data directory is empty")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(abs, "transcode"), nil
}

func resolveScratchDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("playback: scratch directory is empty")
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		// Preserve parent traversal in explicit scratch paths until symlink resolution.
		path = cwd + string(os.PathSeparator) + path
	}
	return filepath.EvalSymlinks(path)
}

// NewSessionsInDir requires an existing directory and locks its inode before
// reconciling session entries. Closing the descriptor releases the flock.
func NewSessionsInDir(transcoder Transcoder, scratchDir string, now func() time.Time, opts ...SessionsOption) (_ *Sessions, resultErr error) {
	dir, err := resolveScratchDir(scratchDir)
	if err != nil {
		return nil, err
	}
	lock, err := openScratchFile(dir, unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock transcode scratch %q: %w", dir, err)
	}
	cleanup := lock.Close
	defer func() {
		if cleanup != nil {
			if err := cleanup(); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	info, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	s := &Sessions{
		transcoder:  transcoder,
		dir:         dir,
		rootLock:    lock,
		rootInfo:    info,
		now:         now,
		bootNonce:   newBootNonce(),
		sessions:    make(map[SessionKey]*hlsSession),
		paths:       make(map[string]*scratchPath),
		cap:         defaultTranscodeCap,
		resumeHints: make(map[SessionKey]resumeHint),
	}
	for _, opt := range opts {
		opt(s)
	}
	// A successful probe also validates writability for direct callers of this
	// constructor. Keep the probe under the lock and outside the session namespace.
	f, err := os.CreateTemp(dir, ".beamfall-write-*")
	if err == nil {
		err = errors.Join(f.Close(), os.Remove(f.Name()))
	}
	if err != nil {
		return nil, err
	}
	if err := s.PurgeOrphanedTranscodeDirs(); err != nil {
		cleanup = s.Close // Retry any removal debt recorded by the startup sweep.
		return nil, err
	}
	cleanup = nil
	return s, nil
}

// ScratchDir returns the resolved root the manager locked at construction.
func (s *Sessions) ScratchDir() string {
	return s.dir
}

// ScratchDirInfo identifies the directory locked at construction.
func (s *Sessions) ScratchDirInfo() fs.FileInfo {
	return s.rootInfo
}

func (s *Sessions) scratchCollisionDir(path string) (bool, error) {
	lstat := os.Lstat
	if s.lstatScratch != nil {
		lstat = s.lstatScratch
	}
	info, err := lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect transcode scratch collision %q: %w", filepath.Base(path), err)
	}
	return info.IsDir(), nil
}

// allocateScratchLocked drops mu for exclusive mkdir. setupMu keeps the
// existing single setup owner while the registry exposes the unresolved claim.
func (s *Sessions) allocateScratchLocked(ctx context.Context, key SessionKey, library store.LibraryID) (*scratchPath, error) {
	allocation := &scratchAllocation{key: key}
	s.allocation = allocation
	defer func() { s.allocation = nil }()
	for !s.closed && !allocation.revoked && ctx.Err() == nil {
		s.nextID++
		name := fmt.Sprintf("session-%s-%d", s.bootNonce, s.nextID)
		if s.paths[name] != nil {
			continue
		}
		p := &scratchPath{name: name, cutoff: make(chan struct{})}
		s.paths[name] = p
		s.mu.Unlock()
		mkdir := os.Mkdir
		if s.mkdirDir != nil {
			mkdir = s.mkdirDir
		}
		path := filepath.Join(s.dir, name)
		err := mkdir(path, 0o700)
		collision := errors.Is(err, fs.ErrExist)
		var collisionDir bool
		var inspectErr error
		if collision {
			collisionDir, inspectErr = s.scratchCollisionDir(path)
		}
		s.mu.Lock()
		if collision {
			if collisionDir || inspectErr != nil {
				p.state = scratchDebt
				p.inspectCollision = inspectErr != nil
			} else {
				delete(s.paths, name)
			}
			s.wakeCloseLocked()
			continue
		}
		if err != nil {
			delete(s.paths, name)
			s.wakeCloseLocked()
			if allocation.revoked {
				return nil, ErrSessionEvicted
			}
			return nil, err
		}
		// Close continues to count this claim through unpublished setup. Its
		// caller publishes or assigns the initial remover under mu.
		p.library = library
		p.state, p.users = scratchLive, 1
		if allocation.revoked {
			err = ErrSessionEvicted
		}
		return p, err
	}
	if allocation.revoked {
		return nil, ErrSessionEvicted
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrSessionEvicted
}

func (s *Sessions) releaseScratch(p *scratchPath) {
	s.mu.Lock()
	p.users--
	var cleanup *sessionTeardown
	if p.users == 0 && p.drained != nil {
		close(p.drained)
		cleanup, p.cleanup = p.cleanup, nil
	}
	s.mu.Unlock()
	if cleanup != nil {
		s.finishTeardown(cleanup)
	}
}

// retryScratch and the sweep claim one path at a time. Pending removals belong
// to their detach owner until that owner publishes success or leaves debt.
func (s *Sessions) claimScratchRemovalLocked(p *scratchPath) {
	p.state = scratchRemoving
	s.removing++
}

func (s *Sessions) removeScratch(p *scratchPath) error {
	path := filepath.Join(s.dir, p.name)
	shouldRemove := true
	var err error
	if p.inspectCollision {
		shouldRemove, err = s.scratchCollisionDir(path)
	}
	if shouldRemove {
		remove := os.RemoveAll
		if s.removeDir != nil {
			remove = s.removeDir
		}
		if removeErr := remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = fmt.Errorf("remove transcode scratch %q: %w", p.name, removeErr)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.paths, p.name)
	} else {
		p.state = scratchDebt
		p.backoff = min(max(time.Second, 2*p.backoff), time.Minute)
		p.retryAt = s.now().Add(p.backoff)
	}
	s.removing--
	s.wakeCloseLocked()
	return err
}

func (s *Sessions) retryScratch(closing bool) error {
	s.mu.Lock()
	var due []*scratchPath
	now := s.now()
	for _, p := range s.paths {
		if p.state == scratchDebt && (closing || !now.Before(p.retryAt)) {
			due = append(due, p)
		}
	}
	s.mu.Unlock()
	var err error
	for _, p := range due {
		s.mu.Lock()
		if (s.closed && !closing) || s.paths[p.name] != p || p.state != scratchDebt || (!closing && s.now().Before(p.retryAt)) {
			s.mu.Unlock()
			continue
		}
		s.claimScratchRemovalLocked(p)
		s.mu.Unlock()
		err = errors.Join(err, s.removeScratch(p))
	}
	return err
}

// PurgeOrphanedTranscodeDirs reconciles all recognizable unowned directories
// and recorded debt against one listing of the locked root.
func (s *Sessions) PurgeOrphanedTranscodeDirs() (result error) {
	s.mu.Lock()
	if s.closed || s.sweeping {
		s.mu.Unlock()
		return errIncompleteScratchSweep
	}
	s.sweeping = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sweeping = false
		if s.closed {
			s.closeSweepErr = result
		}
		s.wakeCloseLocked()
		s.mu.Unlock()
	}()

	readDir := fs.ReadDir
	if s.readScratch != nil {
		readDir = s.readScratch
	}
	entries, err := readDir(os.DirFS(s.dir), ".")
	if err != nil {
		return fmt.Errorf("list transcode scratch: %w", err)
	}

	s.mu.Lock()
	targets := make(map[string]*scratchPath)
	incomplete := false
	for name, p := range s.paths {
		switch p.state {
		case scratchPending, scratchRemoving:
			incomplete = true
		case scratchDebt:
			targets[name] = p
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !sessionDirName.MatchString(name) {
			continue
		}
		switch p := s.paths[name]; {
		case p == nil:
			if entry.IsDir() {
				targets[name] = nil
			}
		case p.state == scratchAllocating:
			incomplete = true
		}
	}
	s.mu.Unlock()

	if incomplete {
		err = errIncompleteScratchSweep
	}
	for name, cutoff := range targets {
		s.mu.Lock()
		p := s.paths[name]
		switch {
		case p == nil && cutoff == nil:
			p = &scratchPath{name: name}
			s.paths[name] = p
		case p == nil:
			s.mu.Unlock()
			continue // the recorded debt was removed
		case p.state == scratchDebt && (cutoff == nil || p == cutoff):
		case p.state == scratchLive:
			s.mu.Unlock()
			continue
		default:
			s.mu.Unlock()
			err = errors.Join(err, errIncompleteScratchSweep)
			continue
		}
		s.claimScratchRemovalLocked(p)
		s.mu.Unlock()
		err = errors.Join(err, s.removeScratch(p))
	}
	return err
}
