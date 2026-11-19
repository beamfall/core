//go:build (linux && !android) || (darwin && !ios)

package ffmpeg

import "syscall"

// SupportsHLSPause reports whether the HLS process group can be paused safely.
// The platform must keep the leader PID reserved through the final group kill.
const SupportsHLSPause = true

func (p *HLSProcess) pauseGroup() bool {
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGSTOP) == nil
}

func (p *HLSProcess) resumeGroup() bool {
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGCONT) == nil
}
