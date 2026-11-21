//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMediaFileFingerprintsRejectFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	for name, fingerprint := range map[string]func(context.Context, string) (string, error){
		"sample": func(ctx context.Context, path string) (string, error) {
			return SampleMediaFileContentChecked(ctx, path, nil)
		},
		"full": HashMediaFileContentContext,
	} {
		t.Run(name, func(t *testing.T) {
			result := make(chan error, 1)
			ctx := t.Context()
			go func() {
				_, err := fingerprint(ctx, path)
				result <- err
			}()

			select {
			case err := <-result:
				if !errors.Is(err, fs.ErrInvalid) {
					t.Fatalf("fingerprint FIFO: got %v, want %v", err, fs.ErrInvalid)
				}
			case <-time.After(time.Second):
				t.Fatal("fingerprint blocked opening a FIFO without a writer")
			}
		})
	}
}
