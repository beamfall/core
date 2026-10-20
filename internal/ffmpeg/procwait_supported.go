//go:build linux || darwin

package ffmpeg

import (
	"fmt"
	"os/exec"
)

// waitHLSProcess observes the leader's exit without reaping it, kills the
// still-identity-bound process group, then lets exec.Cmd reap the leader.
func waitHLSProcess(cmd *exec.Cmd, killTree func()) error {
	if err := waitForProcessExit(cmd.Process.Pid); err != nil {
		killTree()
		_ = cmd.Wait()
		return fmt.Errorf("ffmpeg: wait for process exit: %w", err)
	}
	killTree()
	return cmd.Wait()
}
