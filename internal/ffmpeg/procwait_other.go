//go:build !linux && !darwin

package ffmpeg

// Platforms without a pre-reap exit observer skip residual-group signalling;
// signalling after Wait would risk targeting a recycled process identifier.
func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}
