package scan

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func writeOSHashFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Expected values were computed with an independent big.Int reference
// implementation of the OpenSubtitles algorithm (size + wrapping sum of the
// first and last 64KiB as little-endian uint64 words).
func TestOSHashKnownVectors(t *testing.T) {
	sequential := make([]byte, 16)
	for i := range sequential {
		sequential[i] = byte(i)
	}
	large := append(
		bytes.Repeat([]byte{0x01}, oshashChunkSize),
		bytes.Repeat([]byte{0x02}, 8192)...,
	)
	cases := []struct {
		name string
		data []byte
		want string
	}{
		// 16 bytes 0x00..0x0F: head and tail both sum the whole file.
		{"small", sequential, "2c2824201c181420"},
		// 64KiB of 0x01 then 8KiB of 0x02: head and tail windows differ.
		{"large", large, "4444444444456400"},
		// 3 bytes: the partial word zero-pads.
		{"partial-word", []byte{0xFF, 0x01, 0x02}, "0000000000040401"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OSHash(writeOSHashFile(t, tc.data))
			if err != nil {
				t.Fatalf("OSHash returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("OSHash = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOSHashMissingFile(t *testing.T) {
	if _, err := OSHash(filepath.Join(t.TempDir(), "missing.bin")); err == nil {
		t.Fatal("OSHash on a missing file returned nil error")
	}
}
