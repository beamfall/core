//go:build !darwin && !linux

package ffmpeg

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// SupportsHLSPause reports whether a process group can be suspended while
// keeping its identity reserved until the group is cleaned up at reap.
const SupportsHLSPause = false

func ownsHLSStaging(os.FileInfo) bool { return false }

func makeHLSOutputPipe(string) error                             { return errors.ErrUnsupported }
func readHLSOutputPipe(string, io.Writer, <-chan struct{}) error { return errors.ErrUnsupported }

func pauseProcessGroup(_ *exec.Cmd) bool           { return false }
func resumeProcessGroup(_ *exec.Cmd) bool          { return false }
func terminatePausedProcessGroup(_ *exec.Cmd) bool { return false }
