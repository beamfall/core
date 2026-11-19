//go:build darwin || linux

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// SupportsHLSPause reports whether a process group can be suspended while
// keeping its identity reserved until the group is cleaned up at reap.
const SupportsHLSPause = true

func pauseProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGSTOP) == nil
}

func resumeProcessGroup(cmd *exec.Cmd) bool {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT) == nil
}

func terminatePausedProcessGroup(cmd *exec.Cmd) bool {
	return resumeProcessGroup(cmd) && syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) == nil
}
