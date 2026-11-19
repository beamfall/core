//go:build linux || darwin

package ffmpeg

import (
	"fmt"
	"os/exec"
)

// waitHLSProcess observes the leader's exit without reaping it. killTree
// disarms further signals and kills the group while the leader PID is still
// reserved, even if the exit observer fails.
func waitHLSProcess(cmd *exec.Cmd, killTree func()) error {
	exitErr := waitForProcessExit(cmd.Process.Pid)
	killTree()
	waitErr := cmd.Wait()
	if exitErr != nil {
		return fmt.Errorf("ffmpeg: wait for process exit: %w", exitErr)
	}
	return waitErr
}
