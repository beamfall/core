//go:build !unix

package ffmpeg

import "os/exec"

// configureProcessGroup is a no-op where process groups are not available: the
// supervisor falls back to killing the single process. Kept so run() is
// platform-neutral.
func configureProcessGroup(_ *exec.Cmd) {}

// killProcessGroup kills the single child process where no process-group primitive
// exists; the bounded-output and reap guarantees still hold for the direct child.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
