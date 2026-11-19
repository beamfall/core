//go:build !darwin && !linux

package ffmpeg

import "os/exec"

// SupportsHLSPause reports whether a process group can be suspended while
// keeping its identity reserved until the group is cleaned up at reap.
const SupportsHLSPause = false

func pauseProcessGroup(_ *exec.Cmd) bool           { return false }
func resumeProcessGroup(_ *exec.Cmd) bool          { return false }
func terminatePausedProcessGroup(_ *exec.Cmd) bool { return false }
