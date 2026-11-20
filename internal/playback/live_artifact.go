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
	manager *Sessions
	session *hlsSession
	path    string
	once    sync.Once
}

func (a *LiveArtifact) Release() {
	a.once.Do(func() { a.manager.releaseScratch(a.session.scratch) })
}

func (a *LiveArtifact) evicted() bool {
	a.manager.mu.Lock()
	defer a.manager.mu.Unlock()
	return a.session.state != sessionRunning
}

func (a *LiveArtifact) OpenPlain() (*os.File, fs.FileInfo, error) {
	if a.evicted() {
		return nil, nil, ErrSessionEvicted
	}
	f, info, err := a.session.openArtifact(a.path)
	if a.evicted() {
		if f != nil {
			_ = f.Close()
		}
		return nil, nil, ErrSessionEvicted
	}
	if errors.Is(err, fs.ErrInvalid) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return nil, nil, errors.Join(fs.ErrNotExist, err)
	}
	return f, info, err
}

func (a *LiveArtifact) OpenEncrypted() (*encderiv.LiveFile, error) {
	if a.evicted() {
		return nil, ErrSessionEvicted
	}
	f, err := a.session.openEncryptedArtifact(a.path)
	if a.evicted() || errors.Is(err, encderiv.ErrLiveSessionClosed) {
		if f != nil {
			_ = f.Close()
		}
		return nil, ErrSessionEvicted
	}
	return f, err
}

func (h *hlsSession) openArtifact(path string) (*os.File, fs.FileInfo, error) {
	rel, err := filepath.Rel(h.dir, path)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, nil, fs.ErrInvalid
	}
	return openScratchMember(h.scratch.dir, rel)
}

func openScratchChild(dir *os.File, name string, flags int) (*os.File, error) {
	if !fs.ValidPath(name) || name == "." || filepath.Base(name) != name {
		return nil, fs.ErrInvalid
	}
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Live paths have at most one rung directory. Open both components without
// following symlinks, and use nonblocking mode before checking the file type.
func openScratchMember(dir *os.File, rel string) (*os.File, fs.FileInfo, error) {
	if rung, name, ok := strings.Cut(rel, string(filepath.Separator)); ok {
		rungDir, err := openScratchChild(dir, rung, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return nil, nil, err
		}
		defer rungDir.Close()
		dir, rel = rungDir, name
	}
	f, err := openScratchChild(dir, rel, unix.O_RDONLY)
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

func (p *scratchPath) readRung(rung string) ([]os.DirEntry, error) {
	dir, err := openScratchChild(p.dir, rung, unix.O_RDONLY|unix.O_DIRECTORY)
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
