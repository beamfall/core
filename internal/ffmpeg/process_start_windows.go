//go:build windows

package ffmpeg

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/windows"
)

func ownerProcessStart(pid int) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("opening process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return "", fmt.Errorf("reading process %d start time: %w", pid, err)
	}
	identity := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	return strconv.FormatUint(identity, 16), nil
}
