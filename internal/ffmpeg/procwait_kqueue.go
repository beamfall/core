//go:build darwin && !ios

package ffmpeg

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// waitForProcessExit blocks until pid, an unreaped child of this process, has
// exited. kqueue may report ESRCH once the child is already a zombie. An
// unreaped child's pid cannot be reused, so ESRCH from a pid that is still a
// zombie means it has already exited; ESRCH from a pid that is gone, or was
// never a child, stays an error (CORE-0661).
func waitForProcessExit(pid int) error {
	if pid < 0 {
		return syscall.EINVAL
	}

	kq, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(kq)

	var change unix.Kevent_t
	unix.SetKevent(&change, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	change.Fflags = unix.NOTE_EXIT
	changes := []unix.Kevent_t{change}
	events := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, changes, events, nil)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.ESRCH {
			return exitedBeforeWatch(pid)
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
			return exitedBeforeWatch(pid)
		}
		return nil
	}
}

// exitedBeforeWatch reports whether a pid kqueue answered ESRCH for exited
// before the watch was registered: it is then an unreaped zombie. Anything
// else, such as a pid that no longer exists, keeps the ESRCH.
func exitedBeforeWatch(pid int) error {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(proc.Proc.P_pid) != pid || proc.Proc.P_stat != zombieState {
		return syscall.ESRCH
	}
	return nil
}

// groupQuietProbes is how many listings in a row must find no live member
// before the sweep ends. One listing is not enough on XNU: a parent that exits
// during it can hide a child it forked meanwhile.
const groupQuietProbes = 3

// zombieState is SZOMB from XNU's sys/proc.h.
const zombieState = 5

// groupListsLiveMember reads the kernel's member list for group pgid rather
// than probing with signal 0, which answers EPERM both for a group of zombies
// and for a live member the caller may not signal.
func groupListsLiveMember(pgid int) bool {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
	if err != nil {
		return true
	}
	for i := range procs {
		if procs[i].Proc.P_stat != zombieState {
			return true
		}
	}
	return false
}
