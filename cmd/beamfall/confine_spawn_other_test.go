//go:build !linux

package main

import (
	"os"
	"runtime"
)

// hostCanConfineMedia reports whether this host can carry the mandatory media
// confinement: macOS with sandbox-exec; nothing else.
func hostCanConfineMedia() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat("/usr/bin/sandbox-exec")
	return err == nil
}

// hostHasFilesystemConfinement mirrors hostCanConfineMedia on non-Linux hosts.
func hostHasFilesystemConfinement() bool {
	return hostCanConfineMedia()
}
