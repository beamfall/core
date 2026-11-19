//go:build unix

package playback

import (
	"os"
	"syscall"
)

func openJoinFragment(path string) (*os.File, error) {
	// Open without waiting for a FIFO writer, then check the opened descriptor.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) // #nosec G304 -- path is an encoder segment inside the session's output directory
}
