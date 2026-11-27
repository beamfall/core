package playback

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/encderiv"
	"golang.org/x/sys/unix"
)

// LiveArtifact owns a directory reference for the selected session. Its caller
// must close any opened reader before calling Release.
type LiveArtifact struct {
	manager  *Sessions
	session  *hlsSession
	path     string
	once     sync.Once
	trackRun bool
	version  uint64
	delivery string
	ctx      context.Context
	name     string
	budget   time.Duration
	deadline time.Time
}

type artifactObservation struct {
	path    string
	run     *encoderRun
	version uint64
}

// Bind readiness to the output observed around the confined open. A start
// can advance the version while a requester waits for its first segment.
func (s *Sessions) readyArtifact(h *hlsSession, path string) (artifactObservation, bool) {
	s.mu.Lock()
	artifact := artifactObservation{path: path, run: h.run, version: h.artifactVersion}
	live := h.state == sessionRunning
	s.mu.Unlock()
	if !live || !h.artifactReady(path) {
		return artifactObservation{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return artifact, h.state == sessionRunning && h.artifactVersion == artifact.version &&
		h.run == artifact.run && (h.writing == nil || h.writing == artifact.run)
}

func (a *LiveArtifact) Release() {
	a.once.Do(func() { a.manager.releaseScratch(a.session.scratch) })
}

// Cutoff closes when the exact instance loses byte access.
func (a *LiveArtifact) Cutoff() <-chan struct{} {
	if a.session == nil || a.session.scratch == nil {
		return nil
	}
	return a.session.scratch.cutoff
}

func (a *LiveArtifact) validate(delivered bool) error {
	return a.validateVersion(a.version, delivered)
}

func (a *LiveArtifact) validateVersion(version uint64, delivered bool) error {
	a.manager.mu.Lock()
	defer a.manager.mu.Unlock()
	if a.session.state != sessionRunning {
		return ErrSessionEvicted
	}
	if a.trackRun && a.session.artifactVersion != version {
		return ErrArtifactChanged
	}
	if delivered && a.delivery != "" {
		a.manager.transitionLocked(a.session, deliverSessionRung{id: a.delivery})
	}
	return nil
}

func (a *LiveArtifact) resolve(retry bool) (artifactObservation, error) {
	ctx := a.ctx
	stop, limited := context.CancelFunc(func() {}), false
	defer func() { stop() }()
	for {
		if retry && !limited {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, a.deadline)
			stop, limited = cancel, true
		}
		if err := ctx.Err(); err != nil {
			if validateErr := a.validateVersion(a.version, false); errors.Is(validateErr, ErrSessionEvicted) {
				return artifactObservation{}, validateErr
			}
			return artifactObservation{}, err
		}
		claim, err := a.manager.resolveArtifactPath(ctx, a.session.key, a.session, a.name, a.budget)
		valid := a.validateVersion(claim.version, false)
		if errors.Is(valid, ErrSessionEvicted) {
			return artifactObservation{}, valid
		}
		if errors.Is(err, ErrArtifactChanged) || err == nil && errors.Is(valid, ErrArtifactChanged) {
			retry = true
			continue
		}
		return claim, err
	}
}

func openLiveClaim[T io.Closer](a *LiveArtifact, open func(string) (T, error)) (T, error) {
	claim := artifactObservation{path: a.path, version: a.version}
	var zero T
	for {
		err := a.validateVersion(claim.version, false)
		if err == nil {
			var file T
			file, err = open(claim.path)
			if valid := a.validateVersion(claim.version, err == nil); valid != nil {
				if err == nil {
					_ = file.Close()
				}
				err = valid
			} else {
				if errors.Is(err, fs.ErrNotExist) {
					err = errors.Join(ErrNotFound, err)
				}
				return file, err
			}
		}
		if !errors.Is(err, ErrArtifactChanged) || a.ctx == nil {
			return zero, err
		}
		claim, err = a.resolve(true)
		if err != nil {
			return zero, err
		}
	}
}

func (a *LiveArtifact) OpenPlain() (*os.File, fs.FileInfo, error) {
	var info fs.FileInfo
	f, err := openLiveClaim(a, func(path string) (*os.File, error) {
		file, stat, err := a.session.openArtifact(path)
		info = stat
		if errors.Is(err, fs.ErrInvalid) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			err = errors.Join(fs.ErrNotExist, err)
		}
		return file, err
	})
	if err != nil {
		info = nil
	}
	return f, info, err
}

func (a *LiveArtifact) OpenEncrypted() (*encderiv.LiveFile, error) {
	f, err := openLiveClaim(a, a.session.openEncryptedArtifact)
	if errors.Is(err, encderiv.ErrLiveSessionClosed) {
		return nil, ErrSessionEvicted
	}
	return f, err
}

// Live artifact paths have at most one rung component. The manager owns the
// tree until all references exit, so inspecting the rung before opening the
// file suffices to refuse pre-existing directory links.
func (h *hlsSession) artifactRelativePath(path string) (string, error) {
	rel, err := filepath.Rel(h.dir, path)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return "", fs.ErrInvalid
	}
	if rung, name, ok := strings.Cut(rel, string(filepath.Separator)); ok {
		if filepath.Base(name) != name {
			return "", fs.ErrInvalid
		}
		dir := filepath.Join(h.dir, rung)
		info, err := os.Lstat(dir)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", &os.PathError{Op: "open", Path: dir, Err: unix.ENOTDIR}
		}
	}
	return rel, nil
}

func openScratchFile(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

func (h *hlsSession) openArtifact(path string) (*os.File, fs.FileInfo, error) {
	rel, err := h.artifactRelativePath(path)
	if err != nil {
		return nil, nil, err
	}
	f, err := openScratchFile(filepath.Join(h.dir, rel), unix.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = &os.PathError{Op: "open regular file", Path: rel, Err: fs.ErrInvalid}
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func (h *hlsSession) readRung(rung string) ([]os.DirEntry, error) {
	dir, err := openScratchFile(filepath.Join(h.dir, rung), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

func (h *hlsSession) artifactReady(path string) bool {
	f, info, err := h.openArtifact(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return info.Size() > 0
}

func (h *hlsSession) openEncryptedArtifact(path string) (*encderiv.LiveFile, error) {
	if h.live == nil {
		return nil, fs.ErrInvalid
	}
	rel, err := filepath.Rel(h.dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, fs.ErrInvalid
	}
	f, _, err := h.openArtifact(path)
	if err != nil {
		return nil, err
	}
	return h.live.OpenFile(filepath.ToSlash(rel), f)
}
