//go:build linux && !android

package ffmpeg

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func waitForProcessExit(pid int) error {
	var info unix.Siginfo
	for {
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if err == syscall.EINTR {
			continue
		}
		return err
	}
}

// groupQuietProbes is one on Linux: a fork racing the group KILL is restarted
// by the kernel, so no member can appear after the KILL and one listing that
// finds none live is final.
const groupQuietProbes = 1

// groupListsLiveMember scans /proc for a member of group pgid that is not a
// zombie, or a zombie main thread whose process still runs other threads. A
// member in uninterruptible I/O shows as live until the I/O ends and the
// pending KILL takes it. A failed /proc listing, or a stat read that fails for
// any reason but the process having gone or being another user's, counts as a
// live member. This mirrors integrationhost's scan; ffmpeg is a leaf package
// and cannot import it.
func groupListsLiveMember(pgid int) bool {
	if !procIsOurs() {
		return false // the pids in this /proc are not ours to compare
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true
	}
	for _, entry := range entries {
		if pid, err := strconv.Atoi(entry.Name()); err != nil || pid <= 0 {
			continue
		}
		dir := "/proc/" + entry.Name()
		stat, err := os.ReadFile(dir + "/stat") // #nosec G304 -- a /proc entry this package built
		if err != nil {
			if procPresent(err) && !procHidden(err) {
				return true // cannot tell this process is not a member
			}
			continue
		}
		state, group, ok := parseProcStat(stat)
		if ok && group == pgid && (!procExited(state) || threadGroupRuns(dir)) {
			return true
		}
	}
	return false
}

// procIsOurs reports whether /proc belongs to this process's PID namespace,
// so the pgids it lists are the ones this process sees. A /proc inherited
// from an ancestor namespace numbers processes differently; the sweep then
// cannot tell members apart and is skipped, as it was before CORE-2821.
var procIsOurs = sync.OnceValue(func() bool {
	self, err := os.Readlink("/proc/self")
	return err == nil && self == strconv.Itoa(os.Getpid())
})

// procHidden reports a stat read refused for another user's process, as
// hidepid allows. The supervisor's own descendants stay readable to it.
func procHidden(err error) bool {
	return errors.Is(err, fs.ErrPermission)
}

func procExited(state byte) bool { return state == 'Z' || state == 'X' }

// threadGroupRuns reports whether a process whose main thread is a zombie
// still has a running thread: a main thread that called pthread_exit shows as
// a zombie while the rest of the process runs on, holding its descriptors.
func threadGroupRuns(dir string) bool {
	tasks, err := os.ReadDir(dir + "/task")
	if err != nil {
		return procPresent(err)
	}
	for _, task := range tasks {
		stat, err := os.ReadFile(dir + "/task/" + task.Name() + "/stat") // #nosec G304 -- a /proc entry this package built
		if err != nil {
			if procPresent(err) {
				return true
			}
			continue
		}
		if state, _, ok := parseProcStat(stat); !ok || !procExited(state) {
			return true
		}
	}
	return false
}

// procPresent reports whether a failed /proc read leaves the entry possibly
// live: only a process or thread that has gone answers ENOENT or ESRCH.
func procPresent(err error) bool {
	return !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ESRCH)
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
