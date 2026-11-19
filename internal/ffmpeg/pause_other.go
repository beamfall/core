//go:build !linux && !darwin

package ffmpeg

// SupportsHLSPause reports whether the HLS process group can be paused safely.
const SupportsHLSPause = false

func (*HLSProcess) pauseGroup() bool  { return false }
func (*HLSProcess) resumeGroup() bool { return false }

func (p *HLSProcess) terminateGroupLocked() { p.killTreeLocked() }

// With no pre-reap exit observer, use the direct child's process handle.
func (p *HLSProcess) killGroup() { _ = p.cmd.Process.Kill() }
