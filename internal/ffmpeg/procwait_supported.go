//go:build (linux && !android) || (darwin && !ios)

package ffmpeg

import (
	"errors"
	"fmt"
	"syscall"
	"time"
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

const (
	groupSweepDeadline = time.Second
	groupSweepInterval = 2 * time.Millisecond
)

// groupLister reports whether one listing of group pgid finds a member that
// is not a zombie; a test replaces it to stand in for a member KILL cannot
// end.
var groupLister = groupListsLiveMember

// sweepExitedGroup repeats the group KILL while the group still has a live
// member. XNU can miss a child that is mid-fork when the first group KILL
// lands, leaving it running and orphaned to launchd (CORE-2716). On Linux the
// first KILL reaches every member, but a member in uninterruptible I/O lives
// on, holding the output pipes, until the I/O ends (CORE-2821). The caller
// holds the group leader pgid as an unreaped zombie, so the group ID cannot be
// reused during the sweep. It returns errGroupSurvived, after reporting it, if
// a live member remains at the deadline.
func sweepExitedGroup(pgid int) error {
	deadline := time.Now().Add(groupSweepDeadline)
	for groupHasLiveMember(pgid) {
		if time.Now().After(deadline) {
			return reportGroupSurvivor(fmt.Errorf("%w: process group %d", errGroupSurvived, pgid))
		}
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		time.Sleep(groupSweepInterval)
	}
	return nil
}

// groupHasLiveMember lists group pgid until groupQuietProbes listings in a
// row find no member that is not a zombie. A failed listing counts as a live
// member.
func groupHasLiveMember(pgid int) bool {
	for i := range groupQuietProbes {
		if groupLister(pgid) {
			return true
		}
		if i+1 < groupQuietProbes {
			time.Sleep(groupSweepInterval)
		}
	}
	return false
}
