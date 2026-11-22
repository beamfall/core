//go:build unix

package ffmpeg

import "syscall"

// graphicsOpenFlags opens a graphics track without blocking on a FIFO, so a
// track swapped for one fails the regular-file check instead of hanging.
const graphicsOpenFlags = syscall.O_NONBLOCK
