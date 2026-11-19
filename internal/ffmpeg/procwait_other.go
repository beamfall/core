//go:build !(linux || zos || darwin || dragonfly || freebsd || netbsd || openbsd)

package ffmpeg

// Without a non-reaping observer, HLS waits for and signals only the leader.
var hlsExitObserver func(int) error
