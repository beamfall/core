//go:build darwin || linux

package ffmpeg

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// SupportsHLSPause reports whether a process group can be suspended while
// keeping its identity reserved until the group is cleaned up at reap.
const SupportsHLSPause = true

func ownsHLSStaging(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func makeHLSOutputPipe(path string) error { return unix.Mkfifo(path, 0o600) }

func readHLSOutputPipe(path string, dst io.Writer, exited <-chan struct{}) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	buf := make([]byte, 64<<10)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}} // #nosec G115 -- a kernel file descriptor fits in a pollfd
	wrote := false
	for {
		var n int
		var readErr error
		err := waitHLSOutput(exited, func() bool {
			n, readErr = unix.Read(fd, buf)
			if errors.Is(readErr, unix.EAGAIN) {
				// Wait efficiently while the muxer is writing a large
				// segment, with a bounded wait for process exit.
				if _, err := unix.Poll(poll, 10); err == nil {
					n, readErr = unix.Read(fd, buf)
				}
			}
			switch {
			case n > 0:
				return true
			case errors.Is(readErr, unix.EAGAIN), errors.Is(readErr, unix.EINTR):
				return false
			case readErr != nil:
				return true
			default:
				// With no writer yet, read also returns EOF.
				return wrote
			}
		})
		if err != nil {
			return err
		}
		if n == 0 || readErr != nil {
			return readErr
		}
		if _, err := dst.Write(buf[:n]); err != nil {
			return err
		}
		wrote = true
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
