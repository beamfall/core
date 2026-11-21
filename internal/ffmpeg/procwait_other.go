//go:build !unix

package ffmpeg

import "os/exec"

func (p *HLSProcess) waitForExit() error {
	return p.cmd.Wait()
}

func reapGroup(cmd *exec.Cmd) error {
	return cmd.Wait()
}
