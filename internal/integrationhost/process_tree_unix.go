//go:build unix

package integrationhost

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type workerProcessTree struct {
	cmd  *exec.Cmd
	pgid int
	pin  *ProcessGroupPin
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
	pgid := t.cmd.Process.Pid
	// The worker's own pid pins the group only until cmd.Wait reaps it, which
	// can happen before retire. The pin keeps the ID until retire is done.
	pin, err := PinProcessGroup(pgid)
	if err != nil {
		// The unreaped leader still reserves the ID, so the group can be
		// killed, children forked so far included, before it is reaped.
		killErr := killProcessGroup(pgid, false, workerKillDeadline)
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
		return errors.Join(err, killErr)
	}
	t.pgid, t.pin = pgid, pin
	return nil
}

func (t *workerProcessTree) retire(grace, killDeadline time.Duration) error {
	if t.pgid <= 0 {
		return nil
	}
	defer t.pin.Release()
	_ = syscall.Kill(-t.pgid, syscall.SIGTERM)
	if waitProcessGroupGone(t.pgid, grace) {
		return nil
	}
	if err := KillProcessGroup(t.pgid, false, killDeadline); err != nil {
		return err
	}
	if waitProcessGroupGone(t.pgid, killDeadline) {
		return nil
	}
	return fmt.Errorf("process group %d survived SIGKILL", t.pgid)
}

// KillProcessGroup SIGKILLs every member of process group pgid. With leave
// set, the caller is itself a member and must survive the KILL; it leaves the
// group first where the kill needs more than one signal.
//
// The caller must hold a ProcessGroupPin for pgid until the call returns, so
// every signal sent here reaches the original group, never a reused ID.
//
// One group KILL is not enough on macOS: a member inside fork when the KILL
// arrives can add its child to the group after the kernel has walked the
// members, and that child survives, orphaned to launchd (CORE-2716). There the
// KILL is repeated until probes find no live member, and an error is returned
// if one remains at the timeout. Linux restarts such a fork, so one KILL is
// enough.
func KillProcessGroup(pgid int, leave bool, timeout time.Duration) error {
	if !validProcessGroup(pgid) {
		return fmt.Errorf("invalid process group %d", pgid)
	}
	return killProcessGroup(pgid, leave, timeout)
}

// ProcessGroupPin keeps a process group ID reserved while its holder signals
// the group. On macOS it is an unreaped zombie member; elsewhere it is empty,
// because one KILL is enough and callers already hold a member.
type ProcessGroupPin struct {
	cmd  *exec.Cmd
	once sync.Once
}

// PinProcessGroup pins group pgid, which must be a group of the caller's
// session that still has a member the caller owns. Callers establish the pin
// before admitting the work they will later kill, and refuse it on error.
func PinProcessGroup(pgid int) (*ProcessGroupPin, error) {
	if !validProcessGroup(pgid) {
		return nil, fmt.Errorf("invalid process group %d", pgid)
	}
	return pinProcessGroup(pgid)
}

// Release reaps the pin, freeing the group ID. It is safe to call more than
// once and on a nil pin.
func (p *ProcessGroupPin) Release() {
	if p == nil || p.cmd == nil {
		return
	}
	p.once.Do(func() { _ = p.cmd.Wait() })
}

func (t *workerProcessTree) close() { t.pin.Release() }

func waitProcessGroupGone(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !processGroupHasLiveMember(pgid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
