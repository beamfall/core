//go:build linux || darwin

package ffmpeg

import (
	"runtime"
	"syscall"
	"time"
)

const hlsTermGrace = 200 * time.Millisecond

// SupportsHLSPause reports whether the HLS process group can be paused safely.
// The platform must keep the leader PID reserved through the final group kill.
const SupportsHLSPause = true

func (p *HLSProcess) pauseGroup() bool {
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGSTOP) == nil
}

func (p *HLSProcess) resumeGroup() bool {
	return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGCONT) == nil
}

func (p *HLSProcess) terminateGroupLocked() {
	if runtime.GOOS == "darwin" {
		p.killTreeLocked()
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGTERM)
	p.escalation = time.AfterFunc(hlsTermGrace, p.killTree)
}

func (p *HLSProcess) killGroup() { killProcessGroup(p.cmd) }
