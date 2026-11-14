package store

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CheckLibraryRoots confirms each root exists on the server, is a directory and
// can be read, before a Library is created with it or pointed at it (CORE-1598).
// A refusal is a ValidationError that names the path and the reason in plain
// words, so a client can put it next to the folder field as is.
func CheckLibraryRoots(roots []string) error {
	for _, root := range roots {
		if err := checkLibraryRoot(root); err != nil {
			return err
		}
	}
	return nil
}

func checkLibraryRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return ValidationError("library root is required")
	}
	path := root
	if absolute, err := filepath.Abs(root); err == nil {
		path = filepath.Clean(absolute)
	}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ValidationError("The folder " + path + " doesn't exist on the server.")
	case errors.Is(err, fs.ErrPermission):
		return libraryRootUnreadable(path)
	case err != nil:
		return ValidationError("The server can't open " + path + ".")
	case !info.IsDir():
		return ValidationError(path + " is a file, not a folder.")
	}
	dir, err := os.Open(path)
	if err != nil {
		return libraryRootUnreadable(path)
	}
	defer dir.Close()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return libraryRootUnreadable(path)
	}
	return nil
}

func libraryRootUnreadable(path string) error {
	return ValidationError("The server doesn't have permission to read " + path + ".")
}
