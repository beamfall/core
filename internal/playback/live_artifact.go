package playback

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
}

func (a *LiveArtifact) Release() {
	a.once.Do(func() { a.manager.releaseScratch(a.session.scratch) })
}

// Cutoff closes when the exact instance loses byte access.
func (a *LiveArtifact) Cutoff() <-chan struct{} { return a.session.scratch.cutoff }

func (a *LiveArtifact) validate(delivered bool) error {
	a.manager.mu.Lock()
	defer a.manager.mu.Unlock()
	if a.session.state != sessionRunning {
		return ErrSessionEvicted
	}
	if a.trackRun && a.session.artifactVersion != a.version {
		return ErrArtifactChanged
	}
	if delivered && a.delivery != "" {
		a.manager.transitionLocked(a.session, deliverSessionRung{id: a.delivery})
	}
	return nil
}

func (a *LiveArtifact) OpenPlain() (*os.File, fs.FileInfo, error) {
	if err := a.validate(false); err != nil {
		return nil, nil, err
	}
	f, info, err := a.session.openArtifact(a.path)
	if claimErr := a.validate(err == nil); claimErr != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, nil, claimErr
	}
	if errors.Is(err, fs.ErrInvalid) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return nil, nil, errors.Join(fs.ErrNotExist, err)
	}
	return f, info, err
}

func (a *LiveArtifact) OpenEncrypted() (*encderiv.LiveFile, error) {
	if err := a.validate(false); err != nil {
		return nil, err
	}
	f, err := a.session.openEncryptedArtifact(a.path)
	if claimErr := a.validate(err == nil); claimErr != nil {
		if f != nil {
			_ = f.Close()
		}
		return nil, claimErr
	}
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
