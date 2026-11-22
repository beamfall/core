//go:build unix

package ffmpeg

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenGraphicsRefusesAFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "song.cdg")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := openGraphics(dir, path)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("openGraphics accepted a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openGraphics blocked on a FIFO")
	}
}
