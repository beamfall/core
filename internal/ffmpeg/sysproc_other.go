//go:build !linux

package ffmpeg

import "os/exec"

// applySysProcAttr is a no-op on non-Linux platforms. Linux-specific process
// attributes (Pdeathsig) are set only where the kernel supports them.
func applySysProcAttr(_ *exec.Cmd) {}

// nicePriority returns 0 on non-Linux platforms: FFmpeg niceness is a Linux-only
// resource-governance lever, so generation runs un-niced here.
func nicePriority(_ int) int {
	return 0
}
