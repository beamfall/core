package ffmpeg

import (
	"bytes"
	"os"
	"strconv"
)

func hlsProcessStopped(pid int) (bool, error) {
	status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status") // #nosec G304 -- test-owned PID
	return bytes.Contains(status, []byte("\nState:\tT ")), err
}
