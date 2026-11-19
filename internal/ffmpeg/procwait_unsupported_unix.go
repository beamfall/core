//go:build unix && ((!linux && !darwin) || android || ios)

package ffmpeg

func (*HLSProcess) waitForExit() error {
	return ffmpeg_on_Unix_requires_Linux_or_macOS
}
