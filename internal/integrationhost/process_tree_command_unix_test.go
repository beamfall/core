//go:build unix

package integrationhost

import "os/exec"

func workerCommandForMissingExecutable() *exec.Cmd {
	return exec.Command("/definitely/missing/beamfall-worker")
}
