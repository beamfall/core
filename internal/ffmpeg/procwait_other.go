//go:build !unix

package ffmpeg

func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}

// reapGroup reaps the child and only then bars further kills: Windows kills
// through the process handle, never a reusable ID, so an overflow kill must
// still reach the child while it runs.
func reapGroup(group *processGroup) error {
	err := group.cmd.Wait()
	group.mu.Lock()
	group.reaped = true
	group.mu.Unlock()
	return err
}
