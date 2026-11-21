//go:build darwin || linux

package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHashScanFileFIFOReleasesSlot(t *testing.T) {
	root := t.TempDir()
	const fifoName = "blocked.mp4"
	fifo := filepath.Join(root, fifoName)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()

	result := make(chan error, 1)
	ctx := t.Context()
	go func() {
		_, err := hashScanFile(ctx, fifo, nil)
		result <- err
	}()
	select {
	case err := <-result:
		if !errors.Is(err, errUnstableSample) {
			t.Fatalf("hash FIFO: got %v, want %v", err, errUnstableSample)
		}
	case <-time.After(time.Second):
		// On regression, unblock the reader so later tests can use the slot.
		if writer, err := dir.OpenFile(fifoName, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
			defer writer.Close()
			select {
			case <-result:
			case <-time.After(time.Second):
			}
		}
		t.Fatal("scan hash blocked opening a FIFO without a writer")
	}

	regular := writeScanFile(t, root, "regular.mp4", []byte("media bytes"))
	next, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	fingerprint, err := hashScanFile(next, regular, nil)
	if err != nil || !strings.HasPrefix(fingerprint, "sample1:") {
		t.Fatalf("hash regular file after FIFO: got %q, %v", fingerprint, err)
	}
}
