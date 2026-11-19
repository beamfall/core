//go:build unix

package ffmpeg

import "fmt"

// waitForExit observes the leader's exit without reaping it. killTree kills
// residual group members while the leader PID is still reserved. The OS init
// reaps orphaned descendants.
func (p *HLSProcess) waitForExit() error {
	exitErr := waitForProcessExit(p.cmd.Process.Pid)
	p.killTree()
	waitErr := p.cmd.Wait()
	if exitErr != nil {
		return fmt.Errorf("ffmpeg: wait for process exit: %w", exitErr)
	}
	return waitErr
}
