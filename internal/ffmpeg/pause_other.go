//go:build (!linux && !darwin) || android || ios

package ffmpeg

// SupportsHLSPause reports whether the HLS process group can be paused safely.
const SupportsHLSPause = false

func (*HLSProcess) pauseGroup() bool  { return false }
func (*HLSProcess) resumeGroup() bool { return false }
