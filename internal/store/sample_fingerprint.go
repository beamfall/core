package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
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
	return SampleMediaFileContentChecked(ctx, path, nil)
}

// SampleMediaFileContentChecked lets a caller choose the fingerprint using
// the sample and the same open file, before the final path verification.
func SampleMediaFileContentChecked(ctx context.Context, path string, check SampleFingerprintCheck) (string, error) {
	return SampleMediaFileContentFrom(ctx, path, func() (SampleFile, error) {
		return os.OpenInRoot(filepath.Dir(path), filepath.Base(path))
	}, check)
}

// SampleFingerprintCheck receives the sample and a reader positioned at byte zero.
type SampleFingerprintCheck func(io.Reader, string) (string, error)

// SampleFile allows stat and random-access reads on the same open file.
type SampleFile interface {
	fs.File
	io.ReaderAt
}

// SampleMediaFileContentFrom samples an opened file and verifies that path still names its inode.
func SampleMediaFileContentFrom(ctx context.Context, path string, open func() (SampleFile, error), check SampleFingerprintCheck) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := open()
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	fingerprint, err := SampleMediaFileContentAt(ctx, file, info.Size())
	if err != nil {
		return "", err
	}
	if check != nil {
		fingerprint, err = check(file, fingerprint)
		if err != nil {
			return "", err
		}
	}
	after, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", errors.New("file changed while sampling content")
	}
	return fingerprint, nil
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
		n, err := file.ReadAt(buffer[:length], offset)
		if err != nil && (err != io.EOF || int64(n) != length) {
			return "", err
		}
		if int64(n) != length {
			return "", io.ErrUnexpectedEOF
		}
		_, _ = hash.Write(buffer[:length])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "sample1:" + hex.EncodeToString(hash.Sum(nil)), nil
}
