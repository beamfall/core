package ffmpeg

import (
	"os"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// EnableHLSDescendantReaping makes orphaned encoder descendants children of
// this process so their HLS owner can reap them by PID.
func EnableHLSDescendantReaping() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

const (
	hlsDescendantReapTimeout = time.Second
	linuxProcessParentIndex  = 1
	linuxProcessGroupIndex   = 2
)

func killHLSProcessGroup(cmd *exec.Cmd) func() {
	if cmd.Process == nil {
		return nil
	}
	leader := cmd.Process.Pid
	group := strconv.Itoa(leader)
	// The leader PID is still reserved, so the process group cannot have
	// been recycled. Snapshot it before SIGKILL can make members disappear.
	var members map[int]string
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == leader {
			continue
		}
		fields, err := linuxProcessStatFields(pid)
		if err == nil && fields[linuxProcessGroupIndex] == group {
			if members == nil {
				members = make(map[int]string)
			}
			members[pid] = fields[linuxProcessStartTimeIndex]
		}
	}
	killProcessGroup(cmd)
	if len(members) == 0 {
		return nil
	}
	return func() { reapHLSDescendants(leader, group, members) }
}

func reapHLSDescendants(leader int, group string, members map[int]string) {
	self := os.Getpid()
	deadline := time.Now().Add(hlsDescendantReapTimeout)
	for {
		for pid, start := range members {
			fields, err := linuxProcessStatFields(pid)
			if err != nil || fields[linuxProcessStartTimeIndex] != start || fields[linuxProcessGroupIndex] != group {
				delete(members, pid)
				continue
			}
			parent, _ := strconv.Atoi(fields[linuxProcessParentIndex])
			if parent == self {
				// Target only this adopted descendant. Cmd.Wait owns the leader.
				waited, err := unix.Wait4(pid, nil, unix.WNOHANG, nil)
				if waited == pid || err == unix.ECHILD {
					delete(members, pid)
				}
			} else if parent != leader && members[parent] == "" {
				delete(members, pid)
			}
			// A descendant still parented inside the group can be adopted
			// on the next pass as its parent exits and reparents it.
		}
		if len(members) == 0 || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
