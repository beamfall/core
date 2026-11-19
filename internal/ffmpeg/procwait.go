package ffmpeg

func (p *HLSProcess) waitForExit() error {
	if p.observeExit != nil && p.observeExit(p.cmd.Process.Pid) == nil {
		// Kill residual group members while the leader PID is still reserved.
		// The OS init reaps orphans.
		p.killTree()
	} else {
		// Once Wait can reap the leader, the numeric group ID may be reused.
		// Disable group signals before Wait without interrupting the encoder.
		p.signalMu.Lock()
		p.groupSignals = false
		p.signalMu.Unlock()
	}
	return p.cmd.Wait()
}
