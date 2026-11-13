//go:build darwin

package ffmpeg

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

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
			// NOTE_EXIT cannot be registered on a child that already exited
			// and is waiting to be reaped. The caller has not reaped it yet,
			// so ESRCH here means it has exited; cmd.Wait reaps it.
		}
		return nil
	}
}
