package integrationhost

import (
	"context"
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

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var result error
	canceled := false
	select {
	case result = <-waitDone:
	case <-ctx.Done():
		canceled = true
		result = context.Cause(ctx)
	}
	retireErr := tree.retire(WorkerTerminationGrace, workerKillDeadline)
	if canceled {
		select {
		case <-waitDone:
		case <-time.After(workerKillDeadline):
			if retireErr == nil {
				retireErr = fmt.Errorf("direct worker was not reaped after tree retirement")
			}
		}
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
