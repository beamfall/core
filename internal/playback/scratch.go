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
	root    *os.Root
}

// NewSessions creates the default root on first use.
func NewSessions(transcoder Transcoder, dataDir string, now func() time.Time) (*Sessions, error) {
	dir := filepath.Join(dataDir, "transcode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return NewSessionsInDir(transcoder, dir, now)
}

// NewSessionsInDir requires an existing directory and locks its inode before
// reconciling session entries. Closing the descriptor releases the flock.
func NewSessionsInDir(transcoder Transcoder, scratchDir string, now func() time.Time) (*Sessions, error) {
	dir, err := filepath.Abs(scratchDir)
	if err != nil {
		return nil, err
	}
	// Use the same canonical path for path-based encoders and encderiv.
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	lock, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = root.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("lock transcode scratch %q: %w", dir, err)
	}
	if now == nil {
		now = time.Now
	}
	s := &Sessions{
		transcoder:  transcoder,
		dir:         dir,
		root:        root,
		rootLock:    lock,
		now:         now,
		bootNonce:   newBootNonce(),
		sessions:    make(map[SessionKey]*hlsSession),
		paths:       make(map[string]*scratchPath),
		cap:         defaultTranscodeCap,
		resumeHints: make(map[SessionKey]resumeHint),
	}
	// A successful probe also validates writability for direct callers of this
	// constructor. Keep the probe under the lock and outside the session namespace.
	probe := ".beamfall-write-" + s.bootNonce
	f, err := root.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		err = errors.Join(f.Close(), root.Remove(probe))
	}
	if err != nil {
		return nil, errors.Join(err, root.Close(), lock.Close())
	}
	if err := s.PurgeOrphanedTranscodeDirs(); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// allocateScratchLocked drops mu for exclusive mkdir. setupMu keeps the
// existing single setup owner while the registry exposes the unresolved claim.
func (s *Sessions) allocateScratchLocked(ctx context.Context, library store.LibraryID) (*scratchPath, error) {
	for !s.closed && ctx.Err() == nil {
		s.nextID++
		name := fmt.Sprintf("session-%s-%d", s.bootNonce, s.nextID)
		if s.paths[name] != nil {
			continue
		}
		p := &scratchPath{name: name, cutoff: make(chan struct{})}
		s.paths[name] = p
		s.starting++
		s.mu.Unlock()
		mkdir := s.root.Mkdir
		if s.mkdirDir != nil {
			mkdir = s.mkdirDir
		}
		err := mkdir(name, 0o700)
		created := err == nil
		var root *os.Root
		if created {
			root, err = s.root.OpenRoot(name)
		}
		s.mu.Lock()
		s.starting--
		s.wakeCloseLocked()
		if !created && errors.Is(err, fs.ErrExist) {
			// The session never owned this entry. Its cleanup belongs to the root.
			p.state = scratchDebt
			continue
		}
		if !created {
			delete(s.paths, name)
			return nil, err
		}
		p.library, p.root, p.state, p.users = library, root, scratchLive, 1
		return p, err
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
	var err error
	if p.root != nil {
		err = p.root.Close()
	}
	remove := s.root.RemoveAll
	name := p.name
	if s.removeDir != nil {
		remove = s.removeDir
		name = filepath.Join(s.dir, name)
	}
	removeErr := remove(name)
	if !errors.Is(removeErr, fs.ErrNotExist) {
		err = errors.Join(err, removeErr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	p.root = nil
	if err == nil {
		delete(s.paths, p.name)
	} else {
		p.state = scratchDebt
		p.backoff = min(max(time.Second, 2*p.backoff), time.Minute)
		p.retryAt = s.now().Add(p.backoff)
		err = fmt.Errorf("remove transcode scratch %q: %w", p.name, err)
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
	entries, err := readDir(s.root.FS(), ".")
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
