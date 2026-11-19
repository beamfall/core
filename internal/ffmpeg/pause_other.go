//go:build !linux && !darwin

package ffmpeg

import "os/exec"

// SupportsHLSPause reports whether the HLS process group can be paused safely.
const SupportsHLSPause = false

func pauseProcessGroup(_ *exec.Cmd) bool  { return false }
func resumeProcessGroup(_ *exec.Cmd) bool { return false }
