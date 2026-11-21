package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MediaFileMatchesFingerprint checks the catalog fingerprint against the file
// generation captured by playback, using the catalog's fingerprint format.
func MediaFileMatchesFingerprint(ctx context.Context, path, generation, fingerprint string) (bool, error) {
	sampled := strings.HasPrefix(fingerprint, "sample1:")
	if !sampled && !strings.HasPrefix(fingerprint, "sha256:") {
		return false, nil
	}
	file, err := openRegularMediaFileFollowing(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || SourceFileGeneration(info) != generation {
		return false, err
	}
	var actual string
	if sampled {
		actual, err = SampleMediaFileContentAt(ctx, file, info.Size())
	} else {
		actual, err = HashMediaFileContentReader(ctx, file)
	}
	return actual == fingerprint, err
}

// openRegularMediaFile opens a media file within its parent directory, so a
// symlink cannot lead outside it, and accepts only a regular file.
func openRegularMediaFile(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(path), mediaFileOpenFlags, 0)
	return regularMediaFile(path, file, err)
}

// openRegularMediaFileFollowing opens a media file the way os.Open does,
// following symlinks, and accepts only a regular file.
func openRegularMediaFileFollowing(path string) (*os.File, error) {
	file, err := os.OpenFile(path, mediaFileOpenFlags, 0) // #nosec G304 -- same path trust as the os.Open it replaces
	return regularMediaFile(path, file, err)
}

// regularMediaFile checks the opened descriptor, since the path can change
// after admission. The open does not wait for a FIFO writer, so a swapped-in
// FIFO fails here instead of holding the caller.
func regularMediaFile(path string, file *os.File, err error) (*os.File, error) {
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = &os.PathError{Op: "open", Path: path, Err: fs.ErrInvalid}
	}
	if err == nil {
		err = clearMediaFileNonblock(file)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
