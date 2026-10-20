//go:build unix

package qbittorrent

import (
	"errors"
	"syscall"
)

// isCrossDevice reports whether a link error is EXDEV (the source and
// destination live on different filesystems), which is the signal to fall back
// to a copy. Any other link error is a real failure.
func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
