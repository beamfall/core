//go:build unix && ((!linux && !darwin) || android || ios)

package ffmpeg

import "os/exec"

func (*HLSProcess) waitForExit() error {
	return ffmpeg_on_Unix_requires_Linux_or_macOS
}

func reapGroup(*exec.Cmd) error {
	return ffmpeg_on_Unix_requires_Linux_or_macOS
}
