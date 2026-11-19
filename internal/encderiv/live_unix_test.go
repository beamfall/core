//go:build darwin || linux

package encderiv

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/beamfall/core/internal/libencrypt"
)

func TestLiveSessionOpenRejectsFIFOWithoutWriter(t *testing.T) {
	session := newTestLiveSession(t)
	const rel = testLiveRung + "/seg00000.m4s"
	path := filepath.Join(session.Dir(), rel)
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		file, err := session.open(rel, func(io.ReaderAt, int64, []byte, libencrypt.ObjectContext) (*libencrypt.DecryptReaderV2, error) {
			return nil, errors.New("FIFO reached decryption")
		})
		if file != nil {
			_ = file.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		if !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("open FIFO: %v, want invalid file", err)
		}
	case <-time.After(5 * time.Second):
		// Release the blocked open so failure can drain the session in cleanup.
		writer, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0) // #nosec G304 -- path is the FIFO created by this test
		if err != nil {
			t.Fatalf("open blocked on FIFO and could not release it: %v", err)
		}
		t.Cleanup(func() {
			session.Close()
			_ = writer.Close()
		})
		t.Fatal("open blocked on FIFO with no writer")
	}
}
