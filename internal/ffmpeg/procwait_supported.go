//go:build darwin

package ffmpeg

import "fmt"

// waitForExit observes the leader's exit without reaping it. killTree
// disarms further signals and kills the group while the leader PID is still
// reserved, even if the exit observer fails.
func (p *HLSProcess) waitForExit() error {
	exitErr := waitForProcessExit(p.cmd.Process.Pid)
	p.killTree()
	waitErr := p.cmd.Wait()
	if exitErr != nil {
		return fmt.Errorf("ffmpeg: wait for process exit: %w", exitErr)
	}
	return waitErr
}
