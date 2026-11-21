//go:build unix

package ffmpeg

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInspectStillFileStaysInTheSourceDirectory(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere.nef")
	if err := os.WriteFile(outside, []byte("II*\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "frame.nef")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectStillFile(link); err == nil {
		t.Fatal("a symlink out of the source directory was read")
	}
}

func TestInspectStillFileDoesNotBlockOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "frame.jpg")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		src, err := inspectStillFile(fifo)
		if err == nil && src.preview != nil {
			err = os.ErrInvalid
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("inspecting a FIFO: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("opening a writerless FIFO blocked")
	}
}
