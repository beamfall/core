//go:build (linux && !android) || (darwin && !ios)

package ffmpeg

import (
	"errors"
	"fmt"
)

var hlsExitObserver = waitForProcessExit

func (p *HLSProcess) waitForExit() error {
	observeErr := hlsExitObserver(p.cmd.Process.Pid)
	// Even on observer failure the leader is unreaped, reserving the group
	// ID for the final kill. The OS init reaps orphaned descendants.
	p.killTree()
	sweepErr := sweepExitedGroup(p.cmd.Process.Pid)
	return reapError("encoder", observeErr, sweepErr, p.cmd.Wait())
}

// reapGroup waits for cmd's group leader to exit without reaping it, kills
// whatever is left of its process group, and only then reaps the leader. The
// unreaped (zombie) leader keeps the group ID reserved, so neither the group
// KILL nor the macOS sweep can reach a reused group (CORE-2716). Members still
// running after a natural exit are killed too, as HLSProcess already does.
func reapGroup(group *processGroup) error {
	pid := group.cmd.Process.Pid
	observeErr := waitForProcessExit(pid)
	group.kill()
	sweepErr := sweepExitedGroup(pid)
	return reapError("process", observeErr, sweepErr, group.wait())
}

// reapError reports observer and sweep failures, both when both occur, ahead
// of the child's own exit error.
func reapError(what string, observeErr, sweepErr, waitErr error) error {
	if observeErr == nil && sweepErr == nil {
		return waitErr
	}
	var errs []error
	if observeErr != nil {
		errs = append(errs, fmt.Errorf("ffmpeg: observe %s exit: %w", what, observeErr))
	}
	if sweepErr != nil {
		errs = append(errs, fmt.Errorf("ffmpeg: kill %s group: %w", what, sweepErr))
	}
	return errors.Join(errs...)
}
