//go:build windows

package ffmpeg

import (
	"errors"

	"golang.org/x/sys/windows"
)

func ownerProcessAlive(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false
	}
	if err != nil {
		return true
	}
	defer windows.CloseHandle(process)

	event, err := windows.WaitForSingleObject(process, 0)
	if err != nil {
		return true
	}
	return event != windows.WAIT_OBJECT_0
}
