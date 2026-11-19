//go:build linux || darwin

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// SupportsHLSPause reports whether the HLS process group can be paused safely.
// The platform must keep the leader PID reserved through the final group kill.
const SupportsHLSPause = true

func pauseProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGSTOP) == nil
}

func resumeProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT) == nil
}
