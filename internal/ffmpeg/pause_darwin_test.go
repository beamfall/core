package ffmpeg

import (
	"testing"

	"golang.org/x/sys/unix"
)

// Darwin reparents orphans to launchd, which reaps them.
func becomeHLSStopTestSubreaper(_ *testing.T)            {}
func reapHLSStopTestDescendant(_ *testing.T, _ int) bool { return true }

func hlsProcessStopped(pid int) (bool, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false, err
	}
	const stopped = 4 // SSTOP in <sys/proc.h>
	return info.Proc.P_stat == stopped, nil
}
