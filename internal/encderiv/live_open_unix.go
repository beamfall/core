//go:build unix

package encderiv

import (
	"os"
	"syscall"
)

func openLiveFile(path string) (*os.File, error) {
	// Opening a FIFO must not hold up the session's Close.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0) // #nosec G304 -- path is a validated name inside the session's own directory
}
