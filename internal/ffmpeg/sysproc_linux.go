//go:build linux

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// applySysProcAttr sets OS-level process attributes on cmd. On Linux,
// Pdeathsig ensures the child ffmpeg/ffprobe process is killed with SIGKILL
// if the parent server process exits unexpectedly, preventing orphaned
// transcodes from accumulating.
func applySysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// nicePriority returns the niceness applicable on this platform. On Linux the
// requested value passes through unchanged so background generation FFmpeg runs
// de-prioritized.
func nicePriority(niceness int) int {
	return niceness
}
