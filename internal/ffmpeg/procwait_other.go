//go:build !linux && !darwin

package ffmpeg

import "os/exec"

// Platforms without a pre-reap exit observer skip residual-group signalling;
// signalling after Wait would risk targeting a recycled process identifier.
func waitHLSProcess(cmd *exec.Cmd, _ func()) error {
	return cmd.Wait()
}
