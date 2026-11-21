//go:build darwin && !ios

package ffmpeg

import (
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// waitForProcessExit blocks until pid, an unreaped child of this process, has
// exited. kqueue may report ESRCH once the child is already a zombie. An
// unreaped child's pid cannot be reused, so ESRCH means it has already exited.
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

const (
	groupSweepDeadline = time.Second
	groupSweepInterval = 2 * time.Millisecond
	// groupQuietProbes is how many probes in a row must find no live member
	// before the sweep ends.
	groupQuietProbes = 3
)

// sweepExitedGroup repeats the group KILL while the group still has a live
// member. XNU can miss a child that is mid-fork when the first group KILL
// lands, leaving it running and orphaned to launchd (CORE-2716). The caller
// holds the group leader pgid as an unreaped zombie, so the group ID cannot be
// reused during the sweep. It returns an error if a live member remains at
// the deadline.
func sweepExitedGroup(pgid int) error {
	deadline := time.Now().Add(groupSweepDeadline)
	for groupHasLiveMember(pgid) {
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: process group %d", errGroupSurvived, pgid)
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		time.Sleep(groupSweepInterval)
	}
	return nil
}

// groupHasLiveMember lists group pgid until several listings in a row find
// no member that is not a zombie. It reads the kernel's member list rather
// than probing with signal 0, which answers EPERM both for a group of zombies
// and for a live member the caller may not signal. One listing is not enough:
// a parent that exits during it can hide a child it forked meanwhile. A failed
// listing counts as a live member.
func groupHasLiveMember(pgid int) bool {
	for i := range groupQuietProbes {
		if groupListsLiveMember(pgid) {
			return true
		}
		if i+1 < groupQuietProbes {
			time.Sleep(groupSweepInterval)
		}
	}
	return false
}

// zombieState is SZOMB from XNU's sys/proc.h.
const zombieState = 5

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
