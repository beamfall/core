//go:build darwin || linux

package store

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
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

func TestMediaFileFingerprintsSymlinkOutsideParent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(target, []byte("media content"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "media.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	want, err := HashMediaFileContentContext(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := HashMediaFileContentContext(t.Context(), link); err != nil || got != want {
		t.Fatalf("full hash through symlink = %q, %v; want %q", got, err, want)
	}

	checked := false
	_, err = SampleMediaFileContentChecked(t.Context(), link, func(io.Reader, string) (string, error) {
		checked = true
		return "", nil
	})
	if err == nil || checked {
		t.Fatalf("sample through outside symlink: err %v, check called %v; want refusal before the check", err, checked)
	}
}

func TestMediaFileOpenersLeaveBlockingReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "media.bin")
	if err := os.WriteFile(path, []byte("media content"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func(string) (*os.File, error){
		"confined":  openRegularMediaFile,
		"following": openRegularMediaFileFollowing,
	} {
		t.Run(name, func(t *testing.T) {
			file, err := open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			conn, err := file.SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var flags int
			var flagErr error
			if err := conn.Control(func(fd uintptr) { flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil {
				t.Fatal(err)
			}
			if flagErr != nil {
				t.Fatal(flagErr)
			}
			if flags&unix.O_NONBLOCK != 0 {
				t.Fatal("opened media file is still nonblocking")
			}
		})
	}
}
