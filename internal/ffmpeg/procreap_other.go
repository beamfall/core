//go:build !linux

package ffmpeg

import "os/exec"

// EnableHLSDescendantReaping is a no-op outside Linux. On Darwin, launchd
// adopts and reaps orphaned descendants after the process group is killed.
func EnableHLSDescendantReaping() error { return nil }

func killHLSProcessGroup(cmd *exec.Cmd) func() {
	killProcessGroup(cmd)
	return nil
}
