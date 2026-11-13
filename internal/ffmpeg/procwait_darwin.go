//go:build darwin

package ffmpeg

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// waitForProcessExit blocks until pid, an unreaped child of this process, has
// exited. kqueue refuses EVFILT_PROC with ESRCH once the child is already a
// zombie; an unreaped child's pid cannot be reused, so ESRCH means it has
// already exited, not that it is unknown.
func waitForProcessExit(pid int) error {
	if pid < 0 {
		return syscall.EINVAL
	}

	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)

	changes := []unix.Kevent_t{{
		Ident:  uint64(pid),
		Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ONESHOT,
		Fflags: unix.NOTE_EXIT,
	}}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, changes, events, nil)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.ESRCH {
			return nil // already exited before the watch was registered
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if events[0].Flags&unix.EV_ERROR != 0 {
			eventErrno := events[0].Data
			if eventErrno < 0 {
				return fmt.Errorf("ffmpeg: invalid kqueue errno %d", eventErrno)
			}
			if errno := syscall.Errno(eventErrno); errno != syscall.ESRCH {
				return errno
			}
			return nil // already exited before the watch was registered
		}
		return nil
	}
}
