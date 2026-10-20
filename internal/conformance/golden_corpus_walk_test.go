package conformance

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

func TestFindCorpusOrphansPropagatesWalkError(t *testing.T) {
	want := errors.New("walk failed")
	_, err := findCorpusOrphans("root", nil, func(_ string, callback fs.WalkDirFunc) error {
		return callback("root/unreadable", nil, want)
	}, func(base, path string) (string, error) {
		return path, nil
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestFindCorpusOrphansPropagatesRelativePathError(t *testing.T) {
	want := errors.New("relative failed")
	entry := fakeCorpusDirEntry{name: "orphan.json"}
	_, err := findCorpusOrphans("root", nil, func(_ string, callback fs.WalkDirFunc) error {
		return callback("root/orphan.json", entry, nil)
	}, func(_, _ string) (string, error) {
		return "", want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

type fakeCorpusDirEntry struct{ name string }

func (entry fakeCorpusDirEntry) Name() string         { return entry.name }
func (fakeCorpusDirEntry) IsDir() bool                { return false }
func (fakeCorpusDirEntry) Type() fs.FileMode          { return 0 }
func (fakeCorpusDirEntry) Info() (os.FileInfo, error) { return nil, nil }
