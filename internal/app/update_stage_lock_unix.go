//go:build unix

package app

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockUpdateStageFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func unlockUpdateStageFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
