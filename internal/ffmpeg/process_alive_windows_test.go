//go:build windows

package ffmpeg

import (
	"os"
	"testing"
)

func TestOwnerProcessAliveWindows(t *testing.T) {
	if !ownerProcessAlive(os.Getpid()) {
		t.Fatal("current process reported dead")
	}
	if ownerProcessAlive(1 << 30) {
		t.Fatal("nonexistent process reported alive")
	}
}
