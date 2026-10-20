//go:build unix

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup places the child in its own process group so the
// supervisor can kill the whole tree at once. Setpgid with the default Pgid (0)
// makes the child a new group leader whose group id equals its pid, so
// killProcessGroup can signal -pid to reap every descendant ffmpeg spawns.
// It merges into any SysProcAttr applySysProcAttr already set
// (Linux Pdeathsig), never clobbering the parent-loss death signal.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup SIGKILLs the child's whole process group, then the child
// directly as a backstop. A negative pid targets the group leader and every
// descendant, so a wrapper chain (shim / nice / sandbox-exec) and any process
// ffmpeg itself spawned are all reaped — no orphan survives.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
