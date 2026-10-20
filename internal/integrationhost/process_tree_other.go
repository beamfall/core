//go:build !unix && !windows

package integrationhost

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

type workerProcessTree struct {
	cmd *exec.Cmd
}

func newWorkerProcessTree(cmd *exec.Cmd) (*workerProcessTree, error) {
	return &workerProcessTree{cmd: cmd}, nil
}

func (t *workerProcessTree) start() error { return t.cmd.Start() }

func (t *workerProcessTree) retire(_, _ time.Duration) error {
	if t.cmd.Process != nil {
		err := t.cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}

func (t *workerProcessTree) close() {}
