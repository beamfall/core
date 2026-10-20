//go:build !darwin && !linux

package integrationhost

import "os/exec"

func defaultWorkerSandboxPath() string { return "" }

func sandboxedWorkerCommand(_, _, _ string, _ []string, _ string, _ []string) (*exec.Cmd, error) {
	return nil, ErrWorkerPlatformUnsupported
}
