package store

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSampleMediaFileContentSmallFileEncoding(t *testing.T) {
	const expected = "sample1:c3494ca1a2cf8eeb8a11ded316fb55b83c3bbbedb6313cd50415251e5d09e12f"
	got, err := SampleMediaFileContentAt(t.Context(), strings.NewReader("abc"), 3)
	if err != nil || got != expected {
		t.Fatalf("small file sample = %q, err = %v, want %q", got, err, expected)
	}
}

type sampleReaderAtFunc func([]byte, int64) (int, error)

func (read sampleReaderAtFunc) ReadAt(p []byte, offset int64) (int, error) {
	return read(p, offset)
}

func TestSampleMediaFileContentHonorsReaderAtByteCount(t *testing.T) {
	input := strings.Repeat("x", mediaSampleChunk) + "tail"
	want, err := SampleMediaFileContentAt(t.Context(), strings.NewReader(input), int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		n    int
		err  error
		want error
	}{
		{"complete final chunk with EOF", 4, io.EOF, nil},
		{"short final chunk with EOF", 3, io.EOF, io.EOF},
		{"short final chunk without an error", 3, nil, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := sampleReaderAtFunc(func(p []byte, offset int64) (int, error) {
				if offset < mediaSampleChunk {
					return copy(p, input[offset:]), nil
				}
				return copy(p, input[int(offset):int(offset)+tc.n]), tc.err
			})
			got, err := SampleMediaFileContentAt(t.Context(), reader, int64(len(input)))
			if !errors.Is(err, tc.want) || (tc.want == nil && got != want) || (tc.want != nil && got != "") {
				t.Fatalf("sample = %q, err = %v, want digest %q on success, error %v", got, err, want, tc.want)
			}
		})
	}
}
