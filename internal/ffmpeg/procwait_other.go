//go:build !unix

package ffmpeg

func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}
