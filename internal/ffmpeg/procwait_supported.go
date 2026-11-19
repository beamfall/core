//go:build (linux && !android) || (darwin && !ios)

package ffmpeg

import "fmt"

var hlsExitObserver = waitForProcessExit

func (p *HLSProcess) waitForExit() error {
	observeErr := hlsExitObserver(p.cmd.Process.Pid)
	// Even on observer failure the leader is unreaped, reserving the group
	// ID for the final kill. The OS init reaps orphaned descendants.
	p.killTree()
	waitErr := p.cmd.Wait()
	if observeErr != nil {
		return fmt.Errorf("ffmpeg: observe encoder exit: %w", observeErr)
	}
	return waitErr
}
