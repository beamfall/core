//go:build !unix

package ffmpeg

func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}

func reapGroup(group *processGroup) error {
	return group.wait()
}
