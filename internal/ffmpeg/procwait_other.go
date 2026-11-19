//go:build !unix

package ffmpeg

// Platforms without process groups wait for the direct child.
func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}
