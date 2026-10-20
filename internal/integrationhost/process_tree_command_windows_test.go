//go:build windows

package integrationhost

import "os/exec"

func workerCommandForMissingExecutable() *exec.Cmd {
	return exec.Command(`Z:\definitely\missing\beamfall-worker.exe`)
}
