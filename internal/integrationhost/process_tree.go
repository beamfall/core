package integrationhost

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const (
	// WorkerTerminationGrace is the single grace shared by every member of one
	// worker tree after the group receives TERM.
	WorkerTerminationGrace = 500 * time.Millisecond
	workerKillDeadline     = 2 * time.Second
)

// superviseWorker owns cmd and its complete platform process tree. It always
// retires the tree before returning, including when the direct worker exits
// while descendants remain.
func superviseWorker(ctx context.Context, cmd *exec.Cmd, onStart func()) error {
	tree, err := newWorkerProcessTree(cmd)
	if err != nil {
		return fmt.Errorf("integrationhost: prepare worker containment: %w", err)
	}
	defer tree.close()
	pipes, err := newOutputPipes(cmd)
	if err != nil {
		return fmt.Errorf("integrationhost: worker output pipes: %w", err)
	}
	if err := tree.start(); err != nil {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		pipes.abandon()
		return fmt.Errorf("integrationhost: launch worker: %w", err)
	}
	pipes.started()
	if onStart != nil {
		onStart()
	}

	leader := &leaderWait{cmd: cmd}
	exited := make(chan struct{})
	go func() { leader.awaitExit(); close(exited) }()

	var result error
	canceled := false
	select {
	case <-exited:
	case <-ctx.Done():
		canceled = true
		result = context.Cause(ctx)
	}
	retireErr := tree.retire(WorkerTerminationGrace, workerKillDeadline)
	leaderExited := true
	if canceled {
		select {
		case <-exited:
		case <-time.After(workerKillDeadline):
			if retireErr == nil {
				retireErr = fmt.Errorf("direct worker was not reaped after tree retirement")
			}
			leaderExited = false
			go func() { <-exited; _ = leader.reap() }()
		}
	}
	if leaderExited {
		if waitErr := leader.reap(); !canceled {
			result = waitErr
		}
	}
	if !canceled && leader.observeErr != nil {
		result = leader.observeErr
	}
	if retireErr != nil {
		// A live member may still hold an output pipe; stop reading rather
		// than wait for it.
		pipes.abandon()
		return fmt.Errorf("integrationhost: retire worker tree: %w", retireErr)
	}
	if copyErr := pipes.drain(); result == nil {
		result = copyErr
	}
	return result
}

// leaderWait reaps the direct worker only after retire is done where the
// platform can observe its exit without reaping it, so its process ID, which
// is the group ID, stays reserved while retire signals the group (CORE-2750).
// Where observation is unsupported, the group is pinned another way and the
// leader is reaped at exit, as before.
type leaderWait struct {
	cmd        *exec.Cmd
	reaped     bool
	err        error
	observeErr error
}

// awaitExit returns once the leader has exited, or at once if observing it
// failed. The leader is then left unreaped, still reserving the group ID, so
// retire can kill it and the group safely before it is reaped.
func (w *leaderWait) awaitExit() {
	err := observeLeaderExit(w.cmd.Process.Pid)
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		_ = w.reap()
	case err != nil:
		w.observeErr = fmt.Errorf("observe worker exit: %w", err)
	}
}

// reap reaps the leader once and returns its wait result. It must follow
// awaitExit.
func (w *leaderWait) reap() error {
	if !w.reaped {
		w.err, w.reaped = w.cmd.Wait(), true
	}
	return w.err
}
