//go:build linux

package ffmpeg

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func ownerProcessStart(pid int) (string, error) {
	fields, err := linuxProcessStatFields(pid)
	if err != nil {
		return "", fmt.Errorf("reading process %d start time: %w", pid, err)
	}
	return fields[linuxProcessStartTimeIndex], nil
}

const linuxProcessStartTimeIndex = 19

// linuxProcessStatFields returns /proc stat fields starting at the process
// state. The command name can itself contain spaces and closing parentheses.
func linuxProcessStatFields(pid int) ([]string, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304 -- pid is an internally parsed positive integer, not a path fragment from input
	if err != nil {
		return nil, err
	}
	line := string(stat)
	commandEnd := strings.LastIndexByte(line, ')')
	if commandEnd < 0 {
		return nil, fmt.Errorf("malformed stat")
	}
	fields := strings.Fields(line[commandEnd+1:])
	if len(fields) <= linuxProcessStartTimeIndex {
		return nil, fmt.Errorf("malformed stat")
	}
	return fields, nil
}
