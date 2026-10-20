//go:build darwin

package ffmpeg

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

func ownerProcessStart(pid int) (string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("reading process %d start time: %w", pid, err)
	}
	started := process.Proc.P_starttime
	return strconv.FormatInt(started.Sec, 16) + "." + strconv.FormatInt(int64(started.Usec), 16), nil
}
