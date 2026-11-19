//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package ffmpeg

const supportsHLSGroupKill = false

// Without a pre-reap exit observer, restrict signalling to the process handle.
func (p *HLSProcess) killGroup() { _ = p.cmd.Process.Kill() }

// Platforms without a pre-reap exit observer skip residual-group signalling;
// signalling after Wait would risk targeting a recycled process identifier.
func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}
