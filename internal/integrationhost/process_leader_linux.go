//go:build linux

package integrationhost

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// observeLeaderExit blocks until process pid has exited and leaves it
// unreaped. The zombie keeps the worker's process group ID reserved while
// retire signals the group, so no signal can reach a group that reused the ID
// (CORE-2750).
func observeLeaderExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

const (
	// processGroupProbeInterval spaces the listings that confirm a group has
	// no live member.
	processGroupProbeInterval = 2 * time.Millisecond
	// processGroupQuietProbes is how many listings in a row must find no live
	// member before a group counts as empty.
	processGroupQuietProbes = 3
)

// processGroupHasLiveMember reports whether group pgid has a member that is
// not a zombie. A zombie member is the reserved leader or a reaped-soon child,
// not work still running, so it does not hold up retire. Several listings in
// a row must agree, since /proc is read one directory entry at a time and a
// parent exiting during a listing can hide a child it forked meanwhile. A
// failed listing counts as a live member.
func processGroupHasLiveMember(pgid int) bool {
	if errors.Is(syscall.Kill(groupTarget(pgid), 0), syscall.ESRCH) {
		return false
	}
	for i := range processGroupQuietProbes {
		if groupListsLiveMember(pgid) {
			return true
		}
		if i+1 < processGroupQuietProbes {
			time.Sleep(processGroupProbeInterval)
		}
	}
	return false
}

func groupListsLiveMember(pgid int) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := "/proc/" + entry.Name()
		state, group, ok := procStateAndGroup(dir)
		if ok && group == pgid && (!exited(state) || threadGroupRuns(dir)) {
			return true
		}
	}
	return false
}

func exited(state byte) bool { return state == 'Z' || state == 'X' }

// threadGroupRuns reports whether a process whose main thread is a zombie
// still has a running thread: a main thread that called pthread_exit shows as
// a zombie while the rest of the process runs on.
func threadGroupRuns(dir string) bool {
	tasks, err := os.ReadDir(dir + "/task")
	if err != nil {
		// Only a process that has gone has no task list; any other failure
		// counts as a live member, as a failed listing does.
		return !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ESRCH)
	}
	for _, task := range tasks {
		if state, _, ok := procStateAndGroup(dir + "/task/" + task.Name()); ok && !exited(state) {
			return true
		}
	}
	return false
}

// procStateAndGroup reads the state and process group from dir/stat, for a
// /proc/<pid> or /proc/<pid>/task/<tid> directory. ok is false once the
// process or thread has gone.
func procStateAndGroup(dir string) (state byte, pgid int, ok bool) {
	stat, err := os.ReadFile(dir + "/stat") // #nosec G304 -- a /proc entry this package built
	if err != nil {
		return 0, 0, false
	}
	return parseProcStat(stat)
}

// parseProcStat takes the state and pgrp fields from a /proc/<pid>/stat line,
// "pid (comm) state ppid pgrp ...". comm may itself hold ") ", so the fields
// start after the last ')'.
func parseProcStat(stat []byte) (state byte, pgid int, ok bool) {
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, 0, false
	}
	fields := bytes.Fields(stat[end+1:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return 0, 0, false
	}
	pgid, err := strconv.Atoi(string(fields[2]))
	if err != nil {
		return 0, 0, false
	}
	return fields[0][0], pgid, true
}
