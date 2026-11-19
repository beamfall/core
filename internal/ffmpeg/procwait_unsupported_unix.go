//go:build unix && !linux && !darwin

package ffmpeg

func (*HLSProcess) waitForExit() error {
	return ffmpeg_on_Unix_requires_Linux_or_macOS
}
