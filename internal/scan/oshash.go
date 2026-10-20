package scan

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// oshashChunkSize is the window the OpenSubtitles-style hash sums at each end
// of the file.
const oshashChunkSize = 64 * 1024

// OSHash computes the OpenSubtitles-style 64-bit content hash (ADR-0027 D2
// §7): file size plus the wrapping sum of the first and last 64KiB read as
// little-endian uint64 words, as a lowercase 16-hex string. Files smaller
// than the window sum their whole content twice (head and tail overlap); a
// trailing partial word is zero-padded. Pure file I/O — no FFmpeg.
func OSHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	window := size
	if window > oshashChunkSize {
		window = oshashChunkSize
	}
	hash := uint64(size)
	head := make([]byte, window)
	if _, err := io.ReadFull(f, head); err != nil {
		return "", err
	}
	hash += wordSum(head)
	tail := make([]byte, window)
	if _, err := f.ReadAt(tail, size-window); err != nil && err != io.EOF {
		return "", err
	}
	hash += wordSum(tail)
	return fmt.Sprintf("%016x", hash), nil
}

// wordSum adds data's little-endian uint64 words with wrapping overflow,
// zero-padding a trailing partial word.
func wordSum(data []byte) uint64 {
	var sum uint64
	for len(data) >= 8 {
		sum += binary.LittleEndian.Uint64(data)
		data = data[8:]
	}
	if len(data) > 0 {
		var word [8]byte
		copy(word[:], data)
		sum += binary.LittleEndian.Uint64(word[:])
	}
	return sum
}
