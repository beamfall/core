package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	mediaSampleChunk = 64 << 10
	mediaSampleCount = 14
)

// SampleMediaFileContent reads the sample1 identity from a file.
func SampleMediaFileContent(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return SampleMediaFileContentAt(ctx, file, info.Size())
}

// SampleMediaFileContentAt hashes the big-endian file size and either the
// entire small file or fourteen evenly spaced 64 KiB chunks. The first and
// last chunks touch the file boundaries. At zero-based index i, the offset is
// floor(i * (size - 65536) / 13). Up to 917504 bytes are read per file. The
// result is sample1: followed by the lowercase hex digest. ReadAt leaves the
// file cursor alone.
func SampleMediaFileContentAt(ctx context.Context, file io.ReaderAt, size int64) (string, error) {
	if size < 0 {
		return "", fs.ErrInvalid
	}
	hash := sha256.New()
	var sizeBytes [8]byte
	binary.BigEndian.PutUint64(sizeBytes[:], uint64(size))
	_, _ = hash.Write(sizeBytes[:])

	var buffer [mediaSampleChunk]byte
	for i := int64(0); i < mediaSampleCount; i++ {
		offset := i * mediaSampleChunk
		length := int64(mediaSampleChunk)
		if size > mediaSampleChunk*mediaSampleCount {
			span := size - mediaSampleChunk
			// Divide before multiplying to avoid overflowing large offsets.
			intervals := int64(mediaSampleCount - 1)
			offset = i*(span/intervals) + i*(span%intervals)/intervals
		} else {
			length = min(length, size-offset)
			if length <= 0 {
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, err := file.ReadAt(buffer[:length], offset); err != nil {
			return "", err
		}
		_, _ = hash.Write(buffer[:length])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sample1:" + hex.EncodeToString(hash.Sum(nil)), nil
}
