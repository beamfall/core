package store

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

type cancelOnReadFile struct {
	*os.File
	cancel context.CancelFunc
	bytes  int
}

func (f *cancelOnReadFile) Read(buffer []byte) (int, error) {
	n, err := f.File.Read(buffer)
	f.bytes += n
	f.cancel()
	return n, err
}

func TestHashMediaFileContentCancellationClosesReader(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	const size = 2 * 1024 * 1024
	file, err := os.CreateTemp(t.TempDir(), "media")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.Write(make([]byte, size)); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	reader := &cancelOnReadFile{File: file, cancel: cancel}

	fingerprint, err := hashMediaFileContent(ctx, reader)
	if fingerprint != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("hash = %q, %v, want cancellation without a fingerprint", fingerprint, err)
	}
	if reader.bytes <= 0 || reader.bytes > 128*1024 {
		t.Fatalf("read %d of %d bytes, want one chunk", reader.bytes, size)
	}
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("file.Stat after cancellation = %v, want closed file", err)
	}
}
