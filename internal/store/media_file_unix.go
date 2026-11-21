//go:build unix

package store

import (
	"os"
	"syscall"
)

// Open without waiting for a FIFO writer. Regular files ignore O_NONBLOCK.
const mediaFileOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK
