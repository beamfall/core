//go:build linux

package integrationhost

import (
	"os/exec"
	"syscall"
)

// applyWorkerHardening sets the OS-level orphan guard on the worker command
// (ADR-0058 §4, mirroring internal/ffmpeg/sysproc_linux.go). On Linux,
// Pdeathsig=SIGKILL kills the worker if the server exits unexpectedly, so a
// worker can never outlive and orphan the server — the security-critical
// containment property the supervisor depends on for crash containment.
//
// The command is the mandatory worker-shim wrapper built by workerCommand; the
// shim applies setrlimit then execs the artifact in place (same PID), so this
// Pdeathsig guard binds the artifact too.
func applyWorkerHardening(cmd *exec.Cmd, _ WorkerRlimits) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
