package store

import (
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
