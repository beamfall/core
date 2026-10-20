//go:build linux

package ffmpeg

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func ownerProcessStart(pid int) (string, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304 -- pid is an internally parsed positive integer, not a path fragment from input
	if err != nil {
		return "", fmt.Errorf("reading process %d start time: %w", pid, err)
	}
	commandEnd := strings.LastIndexByte(string(stat), ')')
	if commandEnd < 0 {
		return "", fmt.Errorf("reading process %d start time: malformed stat", pid)
	}
	fields := strings.Fields(string(stat[commandEnd+1:]))
	const startTimeIndex = 19
	if len(fields) <= startTimeIndex {
		return "", fmt.Errorf("reading process %d start time: malformed stat", pid)
	}
	return fields[startTimeIndex], nil
}
