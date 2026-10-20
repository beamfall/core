//go:build linux

package ffmpeg

import (
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
