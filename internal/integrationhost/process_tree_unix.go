//go:build unix

package integrationhost

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type workerProcessTree struct {
	cmd  *exec.Cmd
	pgid int
}

func newWorkerProcessTree(cmd *exec.Cmd) (*workerProcessTree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return &workerProcessTree{cmd: cmd}, nil
}

func (t *workerProcessTree) start() error {
	if err := t.cmd.Start(); err != nil {
		return err
	}
	t.pgid = t.cmd.Process.Pid
	return nil
}

func (t *workerProcessTree) retire(grace, killDeadline time.Duration) error {
	if t.pgid <= 0 {
		return nil
	}
	_ = syscall.Kill(-t.pgid, syscall.SIGTERM)
	if waitProcessGroupGone(t.pgid, grace) {
		return nil
	}
	// Production workers include the shim guardian, which ignores TERM and pins
	// this birth PGID until the single KILL below. No later signal can target a
	// reused group identity.
	_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
	if waitProcessGroupGone(t.pgid, killDeadline) {
		return nil
	}
	return fmt.Errorf("process group %d survived SIGKILL", t.pgid)
}

func (t *workerProcessTree) close() {}

func waitProcessGroupGone(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(-pgid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
