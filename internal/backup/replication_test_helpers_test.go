package backup

import (
	"bytes"
	"io"
)

func ReplicationBytesSource(data []byte) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
}
