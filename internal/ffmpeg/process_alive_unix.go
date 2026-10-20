//go:build unix

package ffmpeg

import (
	"errors"
	"syscall"
)

func ownerProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
