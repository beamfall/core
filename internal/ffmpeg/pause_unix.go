//go:build darwin || linux

package ffmpeg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// SupportsHLSPause reports whether a process group can be suspended while
// keeping its identity reserved until the group is cleaned up at reap.
const SupportsHLSPause = true

func openHLSStage(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

// Open relative to the pinned output directory. A muxer can write here,
// so reject symlinks and avoid waiting on an unexpected FIFO.
func openHLSSidecar(stage *os.File, name string) (*os.File, error) {
	fd, err := openHLSStageEntry(stage, name, unix.S_IFREG)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func openHLSStageEntry(stage *os.File, name string, kind uint32) (int, error) {
	fd, err := unix.Openat(int(stage.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: name, Err: err}
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if kind == unix.S_IFREG && stat.Nlink == 0 {
		// A playlist rename can unlink the previous inode between open
		// and fstat. Let the publisher retry the current name.
		_ = unix.Close(fd)
		return -1, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	if uint32(stat.Mode)&unix.S_IFMT != kind || stat.Nlink != 1 {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("ffmpeg: invalid HLS output file %q", name)
	}
	return fd, nil
}

func makeHLSSegmentGate(path string) error {
	return unix.Mkfifo(path, 0o600)
}

func readHLSSegmentGate(stage *os.File, name string, dst io.Writer, exited <-chan struct{}) error {
	fd, err := openHLSStageEntry(stage, name+".tmp", unix.S_IFIFO)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var buf [64 << 10]byte
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} // #nosec G115 -- a kernel file descriptor fits in a pollfd
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	var wrote, committed bool
	for {
		n, err := unix.Read(fd, buf[:])
		switch {
		case n > 0:
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			wrote = true
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EAGAIN):
			// An open writer can send more data before the next tick.
			if _, err := unix.Poll(poll, 5); err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
		case err != nil:
			return err
		case wrote: // the writer closed the fragment
			return nil
		case committed:
			return fmt.Errorf("ffmpeg: empty HLS segment commit %q", name)
		default:
			// With no bytes read, EOF cannot distinguish an unopened
			// gate from an empty fragment. The rename commits it.
			finished := false
			select {
			case <-exited:
				finished = true
			default:
			}
			var stat unix.Stat_t
			err := unix.Fstatat(int(stage.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
			switch {
			case err == nil:
				// The writer could have opened, written and renamed
				// since the read above. Drain once past the rename.
				committed = true
				continue
			case !errors.Is(err, unix.ENOENT):
				return err
			case finished:
				return io.EOF
			}
			select {
			case <-exited:
			case <-ticker.C:
			}
		}
	}
}

func pauseProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGSTOP) == nil
}

func resumeProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT) == nil
}

func terminatePausedProcessGroup(cmd *exec.Cmd) bool {
	return resumeProcessGroup(cmd) && syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) == nil
}
