//go:build unix

package store

import (
	"os"
	"syscall"
)

// Open without waiting for a FIFO writer.
const mediaFileOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK

// clearMediaFileNonblock restores blocking reads once the file is known to be
// regular, so a network or FUSE filesystem cannot answer a read with EAGAIN.
func clearMediaFileNonblock(file *os.File) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var setErr error
	if err := conn.Control(func(fd uintptr) { setErr = syscall.SetNonblock(int(fd), false) }); err != nil {
		return err
	}
	return setErr
}
