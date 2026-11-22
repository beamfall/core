//go:build darwin && !ios

package ffmpeg

import (
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// waitForProcessExit blocks until pid, an unreaped child of this process, has
// exited. kqueue may report ESRCH once the child is already a zombie. An
// unreaped child's pid cannot be reused, so ESRCH from a pid that is still
// this process's child, zombie or mid-exit, means it has exited; ESRCH from a
// pid that is gone, or was never a child, stays an error (CORE-0661).
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

// exitedBeforeWatch reports whether a pid kqueue answered ESRCH for is a
// child of this process that has exited. XNU refuses the watch once a child
// starts exiting, which can be before it reaches SZOMB, so a child still
// finishing its exit is polled until it is a zombie. A pid that is gone, or
// belongs to another parent, keeps the ESRCH.
func exitedBeforeWatch(pid int) error {
	deadline := time.Now().Add(exitSettleTimeout)
	for {
		proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || int(proc.Proc.P_pid) != pid || int(proc.Eproc.Ppid) != os.Getpid() {
			return syscall.ESRCH
		}
		if proc.Proc.P_stat == zombieState {
			return nil
		}
		if time.Now().After(deadline) {
			return syscall.ESRCH
		}
		time.Sleep(time.Millisecond)
	}
}

// exitSettleTimeout bounds how long a child kqueue refused may take to
// become a zombie.
const exitSettleTimeout = 5 * time.Second

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
