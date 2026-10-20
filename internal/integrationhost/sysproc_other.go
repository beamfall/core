//go:build !linux

package integrationhost

import "os/exec"

// applyWorkerHardening is a no-op on non-Linux platforms because Pdeathsig is
// Linux-specific. Process-tree containment is independent: Unix uses a process
// group plus the shim guardian, and Windows uses a kill-on-close Job Object.
func applyWorkerHardening(_ *exec.Cmd, _ WorkerRlimits) {}
