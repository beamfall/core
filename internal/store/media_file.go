package store

import (
	"io/fs"
	"os"
	"path/filepath"
)

// openRegularMediaFile opens a media file within its parent directory and
// checks the opened descriptor in case the path changed after admission.
func openRegularMediaFile(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.OpenFile(filepath.Base(path), mediaFileOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = &os.PathError{Op: "open", Path: path, Err: fs.ErrInvalid}
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
